"""Verify the acceptance fixture uses real TLS and predictable source content."""
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import unittest
import urllib.error
import urllib.request

import catalog_fixture


class CatalogFixtureTests(unittest.TestCase):
    def test_trusted_https_sources_and_temporary_ca(self):
        with catalog_fixture.serve() as fixture:
            bundle = Path(fixture["ca_bundle"])
            self.assertTrue(bundle.exists())
            trusted = urllib.request.build_opener(
                urllib.request.ProxyHandler({}),
                urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(bundle))),
            )
            for key, name in [("initial", b"Shipped Tool"), ("replacement", b"Replacement Tool"), ("extra", b"Extra Tool")]:
                url = fixture[key].replace("host.docker.internal", "127.0.0.1")
                with trusted.open(url, timeout=5) as response:
                    self.assertEqual(response.status, 200)
                    self.assertIn(name, response.read())
            untrusted = urllib.request.build_opener(
                urllib.request.ProxyHandler({}),
                urllib.request.HTTPSHandler(context=ssl.create_default_context()),
            )
            with self.assertRaises(urllib.error.URLError):
                untrusted.open(fixture["initial"].replace("host.docker.internal", "127.0.0.1"), timeout=5)
        self.assertFalse(bundle.exists())

    def test_private_git_source_requires_token(self):
        with catalog_fixture.serve() as fixture, tempfile.TemporaryDirectory() as directory:
            url = fixture["private"].replace("host.docker.internal", "127.0.0.1")
            environment = dict(os.environ, GIT_SSL_CAINFO=str(fixture["ca_bundle"]), GIT_TERMINAL_PROMPT="0",
                               NO_PROXY="127.0.0.1", no_proxy="127.0.0.1")

            def clone(token, target):
                authenticated = url.replace("https://", f"https://x-access-token:{token}@")
                return subprocess.run(["git", "-c", "http.proxy=", "clone", "-q", authenticated, target],
                                      env=environment, capture_output=True, text=True, check=False)

            denied = clone("wrong-token", str(Path(directory) / "denied"))
            self.assertNotEqual(denied.returncode, 0)
            cloned = clone(fixture["private_token"], str(Path(directory) / "cloned"))
            self.assertEqual(cloned.returncode, 0, cloned.stderr)
            self.assertIn("Private Tool", (Path(directory) / "cloned" / "private.yaml").read_text())
            self.assertTrue(fixture["private_host"].startswith("host.docker.internal:"))
