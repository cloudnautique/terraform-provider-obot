"""Disposable HTTPS catalog fixtures for real Obot acceptance tests."""
import base64
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hmac
import os
from pathlib import Path
import secrets
import ssl
import subprocess
import tempfile
import threading

FIXTURE_ENTRY = ("- name: {name}\n  description: Terraform acceptance fixture\n"
                 "  shortDescription: Fixture\n  icon: test\n  runtime: npx\n"
                 "  npxConfig:\n    package: '@example/terraform-fixture'\n")


def private_repository(root):
    """Create a bare Git repository holding one catalog entry, as a private source would."""
    work = root / "private-work"
    work.mkdir()
    (work / "private.yaml").write_text(FIXTURE_ENTRY.format(name="Private Tool"))
    git = ["git", "-c", "user.name=Terraform", "-c", "user.email=terraform@example.com", "-C", str(work)]
    for args in (["init", "-q", "-b", "main"], ["add", "private.yaml"], ["commit", "-q", "-m", "Private catalog"]):
        subprocess.run(git + args, check=True)
    # Obot requires an owner and a repository in a Git source path.
    subprocess.run(["git", "clone", "-q", "--bare", str(work), str(root / "terraform" / "private.git")], check=True)


@contextmanager
def serve():
    """Serve deterministic catalogs over verified TLS, for fresh containers only.

    Three are public YAML files. A fourth is a Git repository that requires
    the returned token, as a private source behind a Git credential does.
    """
    with tempfile.TemporaryDirectory(prefix="obot-test-catalog-") as directory:
        path = Path(directory)
        cert, key = path / "cert.pem", path / "key.pem"
        subprocess.run([
            "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
            "-keyout", str(key), "-out", str(cert), "-days", "1",
            "-subj", "/CN=host.docker.internal",
            "-addext", "subjectAltName=DNS:host.docker.internal,DNS:localhost,IP:127.0.0.1",
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        bundle = path / "ca-bundle.pem"
        system_ca = Path(os.environ.get("SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt"))
        bundle.write_bytes(system_ca.read_bytes() + b"\n" + cert.read_bytes())

        private_repository(path)
        token = secrets.token_urlsafe(24)

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path.startswith("/terraform/private.git/"):
                    self.git()
                    return
                names = {"/shipped.yaml": "Shipped Tool", "/replacement.yaml": "Replacement Tool",
                         "/extra.yaml": "Extra Tool"}
                name = names.get(self.path)
                if not name:
                    self.send_error(404)
                    return
                body = FIXTURE_ENTRY.format(name=name).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/yaml")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                if not self.path.startswith("/terraform/private.git/"):
                    self.send_error(404)
                    return
                self.git()

            def authorized(self):
                header = self.headers.get("Authorization", "")
                if not header.startswith("Basic "):
                    return False
                try:
                    _, _, password = base64.b64decode(header[6:]).decode().partition(":")
                except ValueError:
                    return False
                return hmac.compare_digest(password, token)

            def git(self):
                """Serve the private repository through git http-backend, requiring the token."""
                if not self.authorized():
                    self.send_response(401)
                    self.send_header("WWW-Authenticate", 'Basic realm="fixture"')
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                route, _, query = self.path.partition("?")
                length = int(self.headers.get("Content-Length") or 0)
                environment = {
                    "GIT_PROJECT_ROOT": str(path), "GIT_HTTP_EXPORT_ALL": "1",
                    "PATH_INFO": route, "QUERY_STRING": query, "REQUEST_METHOD": self.command,
                    "CONTENT_TYPE": self.headers.get("Content-Type", ""), "CONTENT_LENGTH": str(length),
                    "HTTP_CONTENT_ENCODING": self.headers.get("Content-Encoding", ""),
                    "GIT_PROTOCOL": self.headers.get("Git-Protocol", ""),
                    "REMOTE_USER": "x-access-token", "PATH": os.environ.get("PATH", ""),
                }
                result = subprocess.run(["git", "http-backend"], input=self.rfile.read(length),
                                        env=environment, capture_output=True, check=False)
                head, _, body = result.stdout.partition(b"\r\n\r\n")
                status = 200
                headers = []
                for line in head.decode().split("\r\n"):
                    name, _, value = line.partition(":")
                    if name.lower() == "status":
                        status = int(value.split()[0])
                    elif name:
                        headers.append((name, value.strip()))
                self.send_response(status)
                for name, value in headers:
                    self.send_header(name, value)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert, key)
        server.socket = tls.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base = f"https://host.docker.internal:{server.server_port}"
        try:
            yield {"ca_bundle": bundle, "initial": base + "/shipped.yaml",
                   "replacement": base + "/replacement.yaml", "extra": base + "/extra.yaml",
                   "private": base + "/terraform/private.git", "private_host": f"host.docker.internal:{server.server_port}",
                   "private_token": token}
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
