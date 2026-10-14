"""Check forwarding behavior using local sockets; no Docker or internet needed."""
import json
from pathlib import Path
import socket
import socketserver
import struct
import tempfile
import threading
import unittest
from unittest.mock import patch

import discobox_network as network


class AfterEOF(socketserver.BaseRequestHandler):
    def handle(self):
        data = bytearray()
        while chunk := self.request.recv(65536):
            data.extend(chunk)
        self.request.sendall(b"reply:" + data)


class EchoUDP(socketserver.BaseRequestHandler):
    def handle(self):
        data, server = self.request
        server.sendto(data, self.client_address)


class ForwardingTests(unittest.TestCase):
    def running(self, server):
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(thread.join)
        self.addCleanup(server.shutdown)
        return server

    def test_tcp_preserves_binary_response_after_client_half_close(self):
        upstream = self.running(network.TCPServer(("127.0.0.1", 0), AfterEOF))
        relay = network.TCPServer(("127.0.0.1", 0), network.TCP)
        relay.upstream = upstream.server_address
        self.running(relay)
        with socket.create_connection(relay.server_address, timeout=3) as client:
            # Also covers the length-prefixed binary framing used by TCP DNS.
            request = struct.pack("!H", 256) + bytes(range(256))
            client.sendall(request)
            client.shutdown(socket.SHUT_WR)
            response = bytearray()
            while chunk := client.recv(4096):
                response.extend(chunk)
        self.assertEqual(response, b"reply:" + request)

    def test_udp_preserves_large_dns_packets_and_transaction_ids(self):
        upstream = self.running(network.UDPServer(("127.0.0.1", 0), EchoUDP))
        relay = network.UDPServer(("127.0.0.1", 0), network.UDP)
        relay.upstream = upstream.server_address
        self.running(relay)
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
            client.settimeout(3)
            for transaction in (12, 34):
                request = struct.pack("!H", transaction) + b"x" * 12000
                client.sendto(request, relay.server_address)
                response, _ = client.recvfrom(65535)
                self.assertEqual(response, request)

    def test_container_settings_use_discovered_gateway_and_keep_ca_verification(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sandbox, config = root / "sandbox.json", root / "config.json"
            sandbox.touch()
            config.write_text(json.dumps({"gateway": "172.28.0.1", "proxy_port": 18888,
                                          "ca_bundle": "/sandbox/ca.crt"}))
            with patch.object(network, "SANDBOX", sandbox), \
                    patch.object(network, "CONFIG", config), \
                    patch.object(network.subprocess, "run") as run:
                run.return_value.returncode = 0
                arguments = network.docker_arguments()
            self.assertEqual(arguments[:2], ["--dns", "172.28.0.1"])
            self.assertIn("host.docker.internal:host-gateway", arguments)
            self.assertIn("HTTPS_PROXY=http://host.docker.internal:18888", arguments)
            self.assertIn("OBOT_SERVER_DISALLOW_PRIVATE_IPMCP=false", arguments)
            self.assertIn(f"/sandbox/ca.crt:{network.CA_TARGET}:ro", arguments)
            self.assertIn(f"SSL_CERT_FILE={network.CA_TARGET}", arguments)
            with patch.object(network, "SANDBOX", root / "missing"):
                self.assertEqual(network.docker_arguments(), [])


if __name__ == "__main__":
    unittest.main()
