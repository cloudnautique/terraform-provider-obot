"""Disposable HTTPS catalog fixtures for real Obot acceptance tests."""
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import threading


@contextmanager
def serve():
    """Serve two deterministic catalogs over verified TLS, for fresh containers only."""
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

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                names = {"/shipped.yaml": "Shipped Tool", "/replacement.yaml": "Replacement Tool"}
                name = names.get(self.path)
                if not name:
                    self.send_error(404)
                    return
                body = (f"- name: {name}\n  description: Terraform acceptance fixture\n"
                        "  shortDescription: Fixture\n  icon: test\n  runtime: npx\n"
                        "  npxConfig:\n    package: '@example/terraform-fixture'\n").encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/yaml")
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
                   "replacement": base + "/replacement.yaml"}
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
