"""Verify the acceptance fixture uses real TLS and predictable source content."""
from pathlib import Path
import ssl
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
