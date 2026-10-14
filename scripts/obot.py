#!/usr/bin/env python3
"""Manage Obot source, CLI artifacts, and isolated local test instances."""
import argparse
import hashlib
import http.cookiejar
import io
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile

ROOT = Path(__file__).resolve().parents[1]
CACHE = ROOT / ".obot"
REPO = "https://github.com/obot-platform/obot.git"
IMAGE = "ghcr.io/obot-platform/obot"
PROJECT = hashlib.sha256(str(ROOT).encode()).hexdigest()[:10]
EMAIL = "test_obot_admin@example.com"
PASSWORD = "TestObotAdmin123!"
ROLES = {"basic": 4, "owner": 8, "admin": 16, "auditor": 32,
         "power-user-plus": 64, "power-user": 128}
LABEL = "ai.obot.terraform-test-project"


class SetupError(Exception):
    pass


class HTTPFailure(SetupError):
    def __init__(self, method, path, status):
        self.status = status
        super().__init__(f"{method} {path} returned HTTP {status}")


def note(message):
    print(message, file=sys.stderr, flush=True)


def run(args, *, cwd=None, env=None, capture=False):
    result = subprocess.run([str(a) for a in args], cwd=cwd, env=env,
                            text=True, stdout=subprocess.PIPE if capture else sys.stderr,
                            stderr=subprocess.PIPE if capture else None)
    if result.returncode:
        # Commands can carry passwords or API keys. Do not reproduce arguments.
        detail = (result.stderr or "").strip() if capture else ""
        raise SetupError(f"{args[0]} failed (exit {result.returncode})" +
                         (f": {detail}" if detail else ""))
    return result.stdout.strip() if capture else ""


def valid_ref(value):
    if value != "main" and not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?", value):
        raise argparse.ArgumentTypeError("ref must be main or a release tag such as v0.26.3")
    return value


def valid_name(value):
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]{0,39}", value):
        raise argparse.ArgumentTypeError("instance names must be 1-40 lowercase letters, digits, _ or -")
    return value


def source(ref, refresh=False, directory=None):
    directory = directory or os.environ.get("OBOT_SOURCE_DIR")
    if directory:
        path = Path(directory).expanduser().resolve()
        path = Path(run(["git", "-C", path, "rev-parse", "--show-toplevel"], capture=True))
    else:
        path = CACHE / "sources" / ref
        path.parent.mkdir(parents=True, exist_ok=True)
        if not path.exists():
            run(["git", "clone", "--depth", "1", "--branch", ref, REPO, path])
        elif refresh:
            if run(["git", "-C", path, "status", "--porcelain"], capture=True):
                raise SetupError(f"Source checkout has local changes: {path}")
            run(["git", "-C", path, "fetch", "--depth", "1", "origin",
                 "refs/heads/main" if ref == "main" else f"refs/tags/{ref}"])
            run(["git", "-C", path, "checkout", "--detach", "FETCH_HEAD"])
    commit = run(["git", "-C", path, "rev-parse", "HEAD"], capture=True)
    return path, commit


def download(url):
    request = urllib.request.Request(url, headers={"User-Agent": "terraform-provider-obot-testenv"})
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.read()


def release_binary(archive, name, data):
    """Extract only the requested regular file; never unpack archive paths."""
    if name.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
            matches = [f for f in bundle.infolist() if f.filename == data and not f.is_dir()]
            if len(matches) == 1:
                return bundle.read(matches[0])
    else:
        with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as bundle:
            matches = [f for f in bundle.getmembers() if f.name == data and f.isfile()]
            if len(matches) == 1:
                return bundle.extractfile(matches[0]).read()
    raise SetupError(f"Release archive must contain one regular {data} file")


def cli(ref, refresh=False, build=False, directory=None):
    directory = directory or os.environ.get("OBOT_SOURCE_DIR")
    system = platform.system().lower()
    arch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64",
            "amd64": "amd64"}.get(platform.machine().lower())
    if system not in ("linux", "darwin", "windows") or not arch:
        raise SetupError(f"Unsupported CLI platform: {system}/{platform.machine()}")
    filename = "obot.exe" if system == "windows" else "obot"
    if ref == "main" or build or directory:
        path, commit = source(ref, refresh, directory)
        dirty = bool(run(["git", "-C", path, "status", "--porcelain"], capture=True))
        # Never reuse a cached binary for an edited developer checkout.
        key = f"{commit}-dirty-{uuid.uuid4().hex[:8]}" if dirty else commit
        binary = CACHE / "cli" / key / f"{system}-{arch}" / filename
        if refresh or not binary.exists():
            binary.parent.mkdir(parents=True, exist_ok=True)
            run(["go", "build", "-trimpath", "-tags=kustomize_disable_go_plugin_support",
                 "-ldflags", f"-s -w -X github.com/obot-platform/obot/pkg/version.Tag={ref}-{commit[:12]}",
                 "-o", binary, "."], cwd=path)
    else:
        binary = CACHE / "cli" / ref / f"{system}-{arch}" / filename
        if refresh or not binary.exists():
            extension = "zip" if system == "windows" else "tar.gz"
            asset = f"obot_{ref}_{system}_{arch}.{extension}"
            base = f"https://github.com/obot-platform/obot/releases/download/{ref}"
            manifest = download(f"{base}/obot_{ref[1:]}_checksums.txt").decode()
            expected = [line.split()[0] for line in manifest.splitlines()
                        if len(line.split()) == 2 and line.split()[1].lstrip("*") == asset]
            if len(expected) != 1:
                raise SetupError(f"No unique checksum for {asset}")
            archive = download(f"{base}/{asset}")
            if hashlib.sha256(archive).hexdigest() != expected[0]:
                raise SetupError(f"Checksum mismatch for {asset}")
            payload = release_binary(archive, asset, filename)
            binary.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.NamedTemporaryFile(dir=binary.parent, delete=False) as handle:
                handle.write(payload)
                temporary = Path(handle.name)
            temporary.chmod(0o755)
            temporary.replace(binary)
    return binary.resolve()


class API:
    def __init__(self, url, token=None):
        self.url = url.rstrip("/")
        self.token = token
        # Test instances are loopback-only; bypass external HTTP proxies.
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, path, body=None, method=None, form=False):
        headers = {"Origin": self.url}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        data = None
        if body is not None:
            data = urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode()
            headers["Content-Type"] = ("application/x-www-form-urlencoded" if form
                                       else "application/json")
        request = urllib.request.Request(self.url + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=10) as response:
                payload = response.read()
                if response.headers.get_content_type() == "application/json":
                    return json.loads(payload)
                return payload.decode()
        except urllib.error.HTTPError as error:
            # Response bodies can contain secrets. Expose operation and status only.
            error.close()
            raise HTTPFailure(request.get_method(), path, error.code) from None

    def login(self, email, password):
        self.request("/oauth2/start", {"email": email, "password": password, "rd": "/"},
                     form=True)
        me = self.request("/api/me")
        if me.get("email") != email or me.get("requirePasswordChange"):
            raise SetupError(f"Local login failed for {email}")
        return me


def password():
    return os.environ.get("OBOT_TEST_PASSWORD", PASSWORD)


def state_path(name):
    return CACHE / "instances" / name / "instance.json"


def load(name):
    try:
        return json.loads(state_path(name).read_text())
    except FileNotFoundError:
        raise SetupError(f"No instance named {name}; run up first") from None


def save(state):
    path = state_path(state["name"])
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2) + "\n")
    temporary.replace(path)


def inspect(kind, name):
    result = subprocess.run(["docker", kind, "inspect", name], text=True,
                            capture_output=True)
    if result.returncode:
        if "no such" in result.stderr.lower():
            return None
        raise SetupError(f"Cannot inspect Docker {kind}: {result.stderr.strip()}")
    return json.loads(result.stdout)[0]


def owned(kind, name):
    data = inspect(kind, name)
    if data is None:
        return None
    labels = data.get("Config", {}).get("Labels", {}) if kind == "container" else data.get("Labels", {})
    if (labels or {}).get(LABEL) != PROJECT:
        raise SetupError(f"Refusing to manage {name}: Docker ownership label differs")
    return data


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def wait_for(state, timeout):
    api = API(state["url"])
    deadline = time.monotonic() + timeout
    last = "not reachable"
    while time.monotonic() < deadline:
        container = owned("container", state["container"])
        if not container or not container["State"]["Running"]:
            raise SetupError(f"Instance {state['name']} stopped during startup; inspect logs")
        try:
            if api.request("/api/healthz") == "ok":
                return
        except (SetupError, urllib.error.URLError, TimeoutError, ConnectionError) as error:
            last = str(error)
        time.sleep(1)
    raise SetupError(f"Instance {state['name']} not healthy after {timeout}s: {last}")


def start(name, ref, port=0, timeout=180, image=None, email=EMAIL, docker_socket=None):
    docker_socket = docker_socket or os.environ.get("OBOT_TEST_DOCKER_SOCKET", "/var/run/docker.sock")
    if not 0 <= port <= 65535 or timeout <= 0:
        raise SetupError("port must be 0-65535 and timeout must be positive")
    directory = state_path(name).parent
    directory.parent.mkdir(parents=True, exist_ok=True)
    try:
        directory.mkdir()
    except FileExistsError:
        raise SetupError(f"Instance {name} already exists; use status or down") from None
    container = f"obot-tf-{PROJECT}-{name}"
    state = {"name": name, "ref": ref, "container": container,
             "volume": container + "-data", "email": email, "status": "starting"}
    save(state)
    try:
        custom_image = image is not None
        image = image or f"{IMAGE}:{ref}"
        if not custom_image or inspect("image", image) is None:
            run(["docker", "pull", image])
        details = json.loads(run(["docker", "image", "inspect", image], capture=True))[0]
        state["image"] = image
        state["image_id"] = details["Id"]
        state["image_digests"] = details.get("RepoDigests", [])
        port = port or free_port()
        state["url"] = f"http://127.0.0.1:{port}"
        save(state)
        run(["docker", "volume", "create", "--label", f"{LABEL}={PROJECT}", state["volume"]])
        setup_token = uuid.uuid4().hex + uuid.uuid4().hex
        environment = os.environ.copy()
        environment["OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_SETUP_TOKEN"] = setup_token
        command = ["docker", "run", "-d", "--name", container,
                   "--label", f"{LABEL}={PROJECT}",
                   "-p", f"127.0.0.1:{port}:8080",
                   "-v", f"{state['volume']}:/data",
                   "-e", "OBOT_SERVER_ENABLE_AUTHENTICATION=true",
                   "-e", f"OBOT_SERVER_HOSTNAME={state['url']}",
                   "-e", f"OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL={email}",
                   "-e", "OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_SETUP_TOKEN",
                   "-e", "OBOT_SERVER_DEFAULT_MCPCATALOG_PATH=",
                   "-e", "OBOT_SERVER_DEFAULT_SYSTEM_MCPCATALOG_PATH=",
                   "-e", "OBOT_SERVER_DISABLE_UPDATE_CHECK=true"]
        if docker_socket:
            command.extend(["-v", f"{docker_socket}:/var/run/docker.sock"])
        command.append(state["image_id"])
        run(command, env=environment)
        wait_for(state, timeout)
        api = API(state["url"])
        api.request("/api/local-auth/activate", {"setupToken": setup_token})
        api.request("/api/local-auth/change-password", {"password": password()})
        identity = api.request("/api/me")
        if identity.get("email") != email:
            raise SetupError("Initial local owner identity did not match")
        # Controllers create this independently of the HTTP listener.
        deadline = time.monotonic() + timeout
        while True:
            try:
                api.request("/api/mcp-catalogs/default")
                break
            except SetupError:
                if time.monotonic() >= deadline:
                    raise
                time.sleep(1)
        state["status"] = "ready"
        save(state)
        note(f"{name}: ready at {state['url']} ({ref}, local owner {email})")
        return state
    except BaseException:
        # Keep metadata and the stopped/running container for diagnostics.
        state["status"] = "failed"
        save(state)
        note(f"Startup failed; inspect with status {name} and clean up with down {name}.")
        raise


def stop(name):
    state = load(name)
    if owned("container", state["container"]):
        run(["docker", "rm", "-f", state["container"]])
    if owned("volume", state["volume"]):
        run(["docker", "volume", "rm", state["volume"]])
    shutil.rmtree(state_path(name).parent)
    note(f"{name}: removed")


def logs(name):
    state = load(name)
    data = owned("container", state["container"])
    if not data:
        return "No container was created.\n"
    result = subprocess.run(["docker", "logs", "--tail", "200", state["container"]],
                            text=True, capture_output=True)
    text = result.stdout + result.stderr
    for entry in data.get("Config", {}).get("Env", []):
        key, _, value = entry.partition("=")
        if value and any(word in key for word in ("TOKEN", "PASSWORD", "SECRET", "API_KEY")):
            text = text.replace(value, "[REDACTED]")
    text = re.sub(r"ok1-[A-Za-z0-9_-]+", "[REDACTED_API_KEY]", text)
    path = CACHE / "logs" / f"{name}.log"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    note(f"Redacted logs: {path}")
    return text


def session(state, email=None):
    api = API(state["url"])
    identity = api.login(email or state["email"], password())
    return api, identity


def token(state, email=None):
    api, identity = session(state, email)
    # A seeded API key lets the upstream CLI login validate and print a token
    # without opening a browser or depending on a desktop OS keyring in CI.
    key = api.request("/api/api-keys", {
        "name": "Terraform tests " + uuid.uuid4().hex[:8], "canAccessAPI": True})
    binary = cli(state["ref"])
    environment = os.environ.copy()
    environment["OBOT_BASE_URL"] = state["url"] + "/api"
    environment["OBOT_TOKEN"] = key["key"]
    result = subprocess.run([str(binary), "login", "--scope", "api", "--print-token"],
                            env=environment, text=True, capture_output=True, timeout=60)
    if result.returncode:
        raise SetupError("obot login --scope api --print-token failed")
    value = result.stdout.strip()
    if value != key["key"]:
        raise SetupError("obot login returned an unexpected token")
    verified = API(state["url"], value).request("/api/me")
    if verified.get("email") != identity["email"]:
        raise SetupError("API token resolved to a different user")
    return value


def add_user(state, email, role):
    api, _ = session(state)
    api.request("/api/local-auth/users",
                {"email": email, "password": password(), "requirePasswordChange": False})
    user_api = API(state["url"])
    identity = user_api.login(email, password())
    api.request("/api/users/" + identity["id"], {"role": ROLES[role]}, method="PATCH")
    identity = user_api.request("/api/me")
    if identity.get("role") != ROLES[role]:
        raise SetupError(f"Role for {email} did not become {role}")
    note(f"{state['name']}: created {email} with role {role}")


def environment_for(states, email=None):
    records = [{"name": state["name"], "base_url": state["url"] + "/api",
                "email": email or state["email"], "token": token(state, email)} for state in states]
    environment = os.environ.copy()
    environment.update({"OBOT_BASE_URL": records[0]["base_url"],
                        "OBOT_TOKEN": records[0]["token"],
                        "OBOT_TEST_EMAIL": records[0]["email"],
                        "OBOT_TEST_INSTANCES": json.dumps(records),
                        "TF_ACC": "1"})
    # CLI commands run with this environment use the matching cached CLI.
    binary = cli(states[0]["ref"])
    environment["PATH"] = str(binary.parent) + os.pathsep + environment.get("PATH", "")
    return environment


def terraform(name, action, arguments):
    state = load(name)
    config = ROOT / "dev.tfrc"
    if not config.is_file():
        raise SetupError("Run make dev first to build and configure the local provider")
    environment = environment_for([state])
    directory = CACHE / "terraform" / name
    directory.mkdir(parents=True, exist_ok=True)
    # Run each instance in its own root directory so Terraform's default local
    # state path is isolated. Link configuration so edits remain visible.
    example = ROOT / "examples" / "dev"
    suffixes = (".tf", ".tf.json", ".tfvars", ".tfvars.json")
    files = {path.name: path for path in example.iterdir()
             if path.is_file() and path.name.endswith(suffixes)}
    for path in directory.iterdir():
        if path.is_symlink() and path.name.endswith(suffixes) and path.name not in files:
            path.unlink()
    for filename, source_path in files.items():
        destination = directory / filename
        if destination.is_symlink():
            destination.unlink()
        destination.symlink_to(source_path)
    environment["TF_CLI_CONFIG_FILE"] = str(config)
    environment["TF_DATA_DIR"] = str(directory / ".terraform")
    command = ["terraform", f"-chdir={directory}"]
    command.extend(["plan", "-destroy"] if action == "plan-destroy" else [action])
    command.extend(arguments)
    note(f"{name}: terraform {action}; state in {directory}")
    # Inherit the terminal so apply/destroy retain Terraform's approval prompt.
    return subprocess.run(command, env=environment).returncode


def smoke(states):
    records = json.loads(environment_for(states)["OBOT_TEST_INSTANCES"])
    for state, record in zip(states, records):
        me = API(state["url"], record["token"]).request("/api/me")
        if me["email"] != state["email"]:
            raise SetupError("Owner identity differs")
        anonymous = {}
        try:
            anonymous = API(state["url"]).request("/api/me")
        except HTTPFailure as error:
            if error.status not in (401, 403):
                raise
        if anonymous.get("email") == state["email"]:
            raise SetupError("Instance admitted an anonymous request as owner")
        API(state["url"], record["token"]).request("/api/local-auth/users")
        # A second local account exercises password login and role separation.
        add_user(state, "test_obot_user@example.com", "basic")
        basic_key = token(state, "test_obot_user@example.com")
        basic = API(state["url"], basic_key).request("/api/me")
        if basic["role"] != ROLES["basic"]:
            raise SetupError("Basic test user has an unexpected role")
        try:
            API(state["url"], basic_key).request("/api/local-auth/users")
        except HTTPFailure as error:
            if error.status != 403:
                raise
        else:
            raise SetupError("Basic test user could access owner/admin-only APIs")
        for other in records:
            if record["name"] == other["name"]:
                continue
            try:
                me = API(state["url"], other["token"]).request("/api/me")
            except HTTPFailure as error:
                if error.status not in (401, 403):
                    raise
                continue
            if me.get("email") == state["email"]:
                raise SetupError("An API token authenticated against a different instance")
        note(f"{state['name']}: owner login, basic-user login, and token isolation passed")
    if len(states) > 1:
        check_provider_aliases(states, records)



def check_provider_aliases(states, records):
    """Exercise aliases with real plugin processes, outside the reattach harness."""
    with tempfile.TemporaryDirectory(prefix="obot-provider-aliases-") as directory:
        path = Path(directory)
        binary_dir = path / "bin"
        binary_dir.mkdir()
        binary_name = "terraform-provider-obot.exe" if os.name == "nt" else "terraform-provider-obot"
        run(["go", "build", "-o", binary_dir / binary_name, "."], cwd=ROOT)
        config = """
terraform {
  required_providers {
    obot = {
      source = "obot-platform/obot"
    }
  }
}
variable "first_token" {
  type = string
  sensitive = true
}
variable "second_token" {
  type = string
  sensitive = true
}
provider "obot" {
  alias = "first"
  base_url = FIRST_URL
  token = var.first_token
}
provider "obot" {
  alias = "second"
  base_url = SECOND_URL
  token = var.second_token
}
data "obot_current_user" "first" {
  provider = obot.first
}
data "obot_current_user" "second" {
  provider = obot.second
}
output "first_email" {
  value = data.obot_current_user.first.email
}
output "second_email" {
  value = data.obot_current_user.second.email
}
"""
        config = config.replace("FIRST_URL", json.dumps(records[0]["base_url"]))
        config = config.replace("SECOND_URL", json.dumps(records[1]["base_url"]))
        (path / "main.tf").write_text(config)
        override = path / "dev.tfrc"
        override.write_text("provider_installation {\n  dev_overrides {\n"
                            '    "registry.terraform.io/obot-platform/obot" = ' +
                            json.dumps(str(binary_dir)) + "\n  }\n  direct {}\n}\n")
        environment = os.environ.copy()
        environment.pop("TF_REATTACH_PROVIDERS", None)
        environment.update({
            "TF_CLI_CONFIG_FILE": str(override),
            "TF_DATA_DIR": str(path / "terraform-data"),
            "TF_VAR_first_token": records[0]["token"],
            "TF_VAR_second_token": records[1]["token"],
        })
        plan = path / "plan"
        run(["terraform", f"-chdir={path}", "plan", "-input=false", "-out", plan],
            env=environment, capture=True)
        result = json.loads(run(["terraform", f"-chdir={path}", "show", "-json", plan],
                                env=environment, capture=True))
        outputs = result["planned_values"]["outputs"]
        if (outputs["first_email"]["value"] != states[0]["email"] or
                outputs["second_email"]["value"] != states[1]["email"]):
            raise SetupError("Compiled provider aliases connected to the wrong Obot identity")
        note("Compiled provider aliases read the correct identities from both instances.")


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    child_args = []
    if argv and argv[0] in ("exec", "testacc", "terraform") and "--" in argv:
        separator = argv.index("--")
        child_args, argv = argv[separator + 1:], argv[:separator]
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    for command in ("source", "cli"):
        p = sub.add_parser(command)
        p.add_argument("--ref", type=valid_ref, default="main")
        p.add_argument("--refresh", action="store_true")
        p.add_argument("--directory", type=Path, default=os.environ.get("OBOT_SOURCE_DIR"))
        if command == "cli":
            p.add_argument("--build", action="store_true")
    p = sub.add_parser("up")
    p.add_argument("name", type=valid_name)
    p.add_argument("--ref", type=valid_ref, default="main")
    p.add_argument("--port", type=int, default=0)
    p.add_argument("--timeout", type=int, default=180)
    p.add_argument("--email", default=EMAIL)
    p.add_argument("--image")
    p.add_argument("--docker-socket", default=None)
    p = sub.add_parser("terraform")
    p.add_argument("name", type=valid_name)
    p.add_argument("action", choices=("plan", "apply", "plan-destroy", "destroy"))
    for command in ("down", "status", "logs", "env", "token", "user-add", "exec"):
        p = sub.add_parser(command)
        p.add_argument("name", type=valid_name)
        if command in ("token", "exec"):
            p.add_argument("--email")
        if command == "user-add":
            p.add_argument("email")
            p.add_argument("--role", choices=ROLES, default="basic")
    for command in ("smoke", "testacc"):
        p = sub.add_parser(command)
        p.add_argument("--ref", type=valid_ref, default="main")
        p.add_argument("--instances", type=int, default=1)
        p.add_argument("--keep", action="store_true")
        p.add_argument("--timeout", type=int, default=180)
    args = parser.parse_args(argv)
    if args.command == "source":
        path, commit = source(args.ref, args.refresh, args.directory)
        note(f"{args.ref}: {commit}")
        print(path)
    elif args.command == "cli":
        binary = cli(args.ref, args.refresh, args.build, args.directory)
        run([binary, "version"])
        print(binary)
    elif args.command == "up":
        print(json.dumps(start(args.name, args.ref, args.port, args.timeout,
                               args.image, args.email, args.docker_socket), indent=2))
    elif args.command == "down":
        stop(args.name)
    elif args.command == "status":
        state = load(args.name)
        data = owned("container", state["container"])
        state["running"] = bool(data and data["State"]["Running"])
        print(json.dumps(state, indent=2))
    elif args.command == "logs":
        print(logs(args.name), end="")
    elif args.command == "env":
        state = load(args.name)
        binary = cli(state["ref"])
        print("export OBOT_BASE_URL=" + shlex.quote(state["url"] + "/api"))
        print("export PATH=" + shlex.quote(str(binary.parent)) + ':"$PATH"')
        print("unset OBOT_TOKEN")
    elif args.command == "token":
        print(token(load(args.name), args.email))
    elif args.command == "user-add":
        add_user(load(args.name), args.email, args.role)
    elif args.command == "exec":
        command = child_args
        if not command:
            parser.error("exec requires a command after --")
        result = subprocess.run(command, env=environment_for([load(args.name)], args.email))
        return result.returncode
    elif args.command == "terraform":
        return terraform(args.name, args.action, child_args)
    elif args.command in ("smoke", "testacc"):
        if args.instances < 1:
            parser.error("--instances must be positive")
        states, names = [], []
        try:
            cli(args.ref)
            for index in range(args.instances):
                name = f"test-{uuid.uuid4().hex[:8]}-{index}"
                names.append(name)
                email = EMAIL if index == 0 else f"test_obot_admin_{index + 1}@example.com"
                states.append(start(name, args.ref, timeout=args.timeout, email=email))
            if args.command == "smoke":
                smoke(states)
            else:
                command = child_args
                command = command or ["go", "test", "-count=1", "-timeout=60m", "-run=TestAcc", "./internal/provider/..."]
                result = subprocess.run(command, env=environment_for(states))
                if result.returncode:
                    for name in names:
                        logs(name)
                return result.returncode
        except BaseException:
            for name in names:
                if state_path(name).exists():
                    try:
                        logs(name)
                    except (SetupError, OSError):
                        pass
            raise
        finally:
            if args.keep:
                note("Kept instances: " + ", ".join(names))
            else:
                for name in reversed(names):
                    if state_path(name).exists():
                        stop(name)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (SetupError, urllib.error.URLError, TimeoutError, ConnectionError, OSError,
            subprocess.TimeoutExpired) as error:
        note(f"Error: {error}")
        sys.exit(1)
