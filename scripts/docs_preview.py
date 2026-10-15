#!/usr/bin/env python3
"""Preview generated provider Markdown with the repository's stable light theme."""
import argparse
from html import escape
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
from urllib.parse import quote, unquote, urlsplit

import markdown
from pygments.formatters import HtmlFormatter

SCRIPTS = Path(__file__).resolve().parent
DOCS = SCRIPTS.parent / "docs"
GROUPS = [("", "Provider"), ("guides", "Guides"), ("resources", "Resources"),
          ("data-sources", "Data sources")]


def navigation(current):
    result = '<h1>Obot Terraform</h1><p>Local documentation preview</p>'
    for folder, label in GROUPS:
        files = sorted((DOCS / folder).glob("*.md")) if folder else [DOCS / "index.md"]
        result += f"<h2>{label}</h2>"
        for item in files:
            relative = item.relative_to(DOCS).as_posix()
            name = "Provider configuration" if not folder else item.stem.replace("-", " ")
            active = ' class="active" aria-current="page"' if item == current else ""
            result += f'<a{active} href="/{quote(relative)}">{escape(name)}</a>'
    return result


def render(target):
    source = re.sub(r"\A---\r?\n.*?\r?\n---\r?\n", "", target.read_text(),
                    count=1, flags=re.S)
    heading = re.search(r"^# (.+)$", source, re.M)
    title = heading.group(1) if heading else target.stem
    content = markdown.markdown(source, extensions=["fenced_code", "tables", "toc", "codehilite"],
                                extension_configs={"codehilite": {"guess_lang": False}})
    # Keep syntax highlighting and theme fixed; never derive colors from the desktop theme.
    style = (HtmlFormatter(style="default").get_style_defs(".codehilite") +
             SCRIPTS.joinpath("docs_preview.css").read_text())
    return f'''<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light">
<title>{escape(title)} — Obot provider docs</title><style>{style}</style></head>
<body><nav aria-label="Documentation">{navigation(target)}</nav>
<main><div class="source">{escape(target.relative_to(DOCS).as_posix())}</div>
{content}</main></body></html>'''


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        relative = unquote(urlsplit(self.path).path).lstrip("/") or "index.md"
        target = (DOCS / relative).resolve()
        if not target.is_relative_to(DOCS) or target.suffix != ".md" or not target.is_file():
            self.send_error(404)
            return
        body = render(target).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, choices=range(1, 65536), default=8765,
                        metavar="PORT")
    args = parser.parse_args()
    print(f"Documentation preview: http://localhost:{args.port}/", flush=True)
    with ThreadingHTTPServer((args.host, args.port), Handler) as server:
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass


if __name__ == "__main__":
    main()
