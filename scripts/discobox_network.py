#!/usr/bin/env python3
"""Bootstrap nested Docker DNS, proxy forwarding, and CA trust in Discobox."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import select
import socket
import socketserver
import struct
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import urlsplit

SANDBOX = Path("/etc/discobox/sandbox.json")
DIRECTORY = Path("/var/lib/obot-discobox-network")
CONFIG = DIRECTORY / "config.json"
UNIT = "obot-discobox-network.service"
PROXY_PORT = 18888
CA_TARGET = "/etc/ssl/certs/ca-certificates.crt"


class BootstrapError(Exception):
    pass


class TCP(socketserver.BaseRequestHandler):
    """Relay bytes, preserving half-close so replies after request EOF survive."""
    def handle(self):
        try:
            with socket.create_connection(self.server.upstream, timeout=10) as remote:
                self.request.settimeout(10)
                readers = [self.request, remote]
                while readers:
                    ready, _, _ = select.select(readers, [], [], 60)
                    if not ready:
                        return
                    for source in ready:
                        target = remote if source is self.request else self.request
                        data = source.recv(65536)
                        if data:
                            target.sendall(data)
                        else:
                            readers.remove(source)
                            with contextlib.suppress(OSError):
                                target.shutdown(socket.SHUT_WR)
        except OSError:
            # No payloads, proxy credentials, or request headers in service logs.
            return


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        data, server = self.request
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as upstream:
                upstream.settimeout(5)
                upstream.connect(self.server.upstream)
                upstream.send(data)
                server.sendto(upstream.recv(65535), self.client_address)
        except OSError:
            return


class TCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class UDPServer(socketserver.ThreadingUDPServer):
    allow_reuse_address = True
    daemon_threads = True
    max_packet_size = 65535


def serve(config):
    address = config["gateway"]
    with UDPServer((address, 53), UDP) as udp, \
            TCPServer((address, 53), TCP) as dns, \
            TCPServer((address, config["proxy_port"]), TCP) as proxy:
        udp.upstream = dns.upstream = tuple(config["resolver"])
        proxy.upstream = tuple(config["proxy_upstream"])
        for server in (udp, dns):
            threading.Thread(target=server.serve_forever, daemon=True).start()
        print(f"DNS: {address}:53 (UDP/TCP); proxy: {address}:{config['proxy_port']}", flush=True)
        proxy.serve_forever()


def discover():
    network = json.loads(subprocess.check_output(
        ["docker", "network", "inspect", "bridge"], text=True))[0]
    gateway = next((entry["Gateway"] for entry in network["IPAM"]["Config"]
                    if "." in entry.get("Gateway", "")), None)
    if not gateway:
        raise BootstrapError("Docker's default bridge has no IPv4 gateway")
    resolver = next((line.split()[1] for line in Path("/etc/resolv.conf").read_text().splitlines()
                     if line.split()[:1] == ["nameserver"] and "." in line.split()[1]), None)
    if not resolver:
        raise BootstrapError("The sandbox has no IPv4 DNS resolver")
    proxy = urlsplit(os.environ.get("HTTPS_PROXY", os.environ.get("https_proxy", "")))
    if proxy.scheme != "http" or not proxy.hostname or proxy.username or proxy.password:
        raise BootstrapError("HTTPS_PROXY must name the sandbox's HTTP proxy without embedded credentials")
    ca = Path(os.environ.get("SSL_CERT_FILE", CA_TARGET)).resolve()
    if not ca.is_file():
        raise BootstrapError("The sandbox CA bundle does not exist; check SSL_CERT_FILE")
    return {"gateway": gateway, "resolver": [resolver, 53],
            "proxy_upstream": [proxy.hostname, proxy.port or 80],
            "proxy_port": PROXY_PORT, "ca_bundle": str(ca)}


def dns_query(host):
    question = b"".join(bytes([len(label)]) + label.encode() for label in host.split("."))
    return struct.pack("!HHHHHH", 12345, 0x100, 1, 0, 0, 0) + question + b"\0\0\1\0\1"


def ready(config):
    address = config["gateway"]
    # Check the DNS relay itself, rather than relying only on a listening port.
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as dns:
        dns.settimeout(5)
        dns.sendto(dns_query("github.com"), (address, 53))
        response, _ = dns.recvfrom(65535)
        if len(response) < 12 or response[3] & 15 or response[6:8] == b"\0\0":
            raise BootstrapError("Forwarded github.com DNS lookup returned no successful answer")
    for port in (53, config["proxy_port"]):
        with socket.create_connection((address, port), timeout=2):
            pass


def bootstrap():
    if not SANDBOX.is_file():
        raise BootstrapError("discobox-bootstrap only applies inside Discobox")
    config = discover()
    unit = f"""[Unit]
Description=Obot development DNS and proxy forwarding for nested Docker
After=docker.service network.target
Requires=docker.service

[Service]
Type=simple
ExecStart={sys.executable} {DIRECTORY}/forward.py serve
Restart=on-failure
RestartSec=1
DynamicUser=yes
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
"""
    changed = False
    with tempfile.TemporaryDirectory() as temporary:
        temporary = Path(temporary)
        sources = {DIRECTORY / "forward.py": Path(__file__).read_bytes(),
                   CONFIG: (json.dumps(config, indent=2) + "\n").encode(),
                   Path("/etc/systemd/system") / UNIT: unit.encode()}
        subprocess.run(["sudo", "install", "-d", "-m", "755", str(DIRECTORY)], check=True)
        for index, (target, contents) in enumerate(sources.items()):
            if target.is_file() and target.read_bytes() == contents:
                continue
            source = temporary / str(index)
            source.write_bytes(contents)
            subprocess.run(["sudo", "install", "-m", "644", str(source), str(target)], check=True)
            changed = True
    subprocess.run(["sudo", "systemctl", "daemon-reload"], check=True)
    subprocess.run(["sudo", "systemctl", "enable", UNIT], check=True)
    subprocess.run(["sudo", "systemctl", "restart" if changed else "start", UNIT], check=True)
    deadline = time.monotonic() + 20
    while True:
        try:
            if subprocess.run(["systemctl", "is-active", "--quiet", UNIT]).returncode:
                raise BootstrapError("The forwarding service is not active")
            ready(config)
            break
        except (OSError, BootstrapError):
            if time.monotonic() >= deadline:
                raise BootstrapError(f"Networking failed its readiness check; inspect: journalctl -u {UNIT}") from None
            time.sleep(1)
    print(f"Discobox networking ready: DNS {config['gateway']}:53, proxy port {config['proxy_port']}")
    print("New Obot containers will use these settings. Recreate existing disposable instances to pick them up.")


def docker_arguments():
    if not SANDBOX.is_file():
        return []
    try:
        config = json.loads(CONFIG.read_text())
    except FileNotFoundError:
        raise BootstrapError("Run make discobox-bootstrap before starting Obot inside Discobox") from None
    if subprocess.run(["systemctl", "is-active", "--quiet", UNIT]).returncode:
        raise BootstrapError("Discobox networking is stopped; run make discobox-bootstrap")
    proxy = f"http://host.docker.internal:{config['proxy_port']}"
    bypass = "localhost,127.0.0.1,::1,host.docker.internal," + config["gateway"]
    arguments = ["--dns", config["gateway"], "--add-host", "host.docker.internal:host-gateway",
                 "-v", f"{config['ca_bundle']}:{CA_TARGET}:ro"]
    for key, value in {"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy,
                       "http_proxy": proxy, "https_proxy": proxy,
                       "NO_PROXY": bypass, "no_proxy": bypass,
                       # Obot's HTTP dialer applies this check to the proxy
                       # address too, which is necessarily a private bridge IP.
                       "OBOT_SERVER_DISALLOW_PRIVATE_IPMCP": "false",
                       "SSL_CERT_FILE": CA_TARGET, "NODE_EXTRA_CA_CERTS": CA_TARGET,
                       "REQUESTS_CA_BUNDLE": CA_TARGET}.items():
        arguments.extend(["-e", f"{key}={value}"])
    return arguments


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("bootstrap", "serve"))
    args = parser.parse_args()
    try:
        if args.command == "bootstrap":
            bootstrap()
        else:
            serve(json.loads(CONFIG.read_text()))
    except (BootstrapError, OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Discobox networking: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
