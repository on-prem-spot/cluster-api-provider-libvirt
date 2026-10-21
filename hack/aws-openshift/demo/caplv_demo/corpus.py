# Copyright 2026 Anthony Green.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Build the demo corpus: the Kubernetes documentation, split into chunks.

Run at image build time on a sparse checkout of kubernetes/website; writes
one JSON object per chunk (id, title, url, text) to a gzipped JSONL file.
"""

import gzip
import hashlib
import json
import re
import sys
from pathlib import Path

CHUNK_CHARS = 900
FRONT_MATTER = re.compile(r"\A---\n(.*?)\n---\n", re.S)
SHORTCODE = re.compile(r"\{\{[<%].*?[%>]\}\}", re.S)


def title_of(front_matter: str, path: Path) -> str:
    for line in front_matter.splitlines():
        if line.startswith("title:"):
            return line.split(":", 1)[1].strip().strip("\"'")
    return path.stem.replace("-", " ").title()


def chunks(text: str):
    """Split on blank lines, packing paragraphs into chunks of about CHUNK_CHARS."""
    current = ""
    for para in re.split(r"\n\s*\n", text):
        para = para.strip()
        if not para:
            continue
        if current and len(current) + len(para) > CHUNK_CHARS:
            yield current
            current = ""
        current = f"{current}\n\n{para}" if current else para
        while len(current) > 2 * CHUNK_CHARS:
            yield current[:CHUNK_CHARS]
            current = current[CHUNK_CHARS:]
    if len(current) > 80:
        yield current


def main(docs_root: str, out: str) -> None:
    root = Path(docs_root)
    count = 0
    with gzip.open(out, "wt", encoding="utf-8") as f:
        for path in sorted(root.rglob("*.md")):
            raw = path.read_text(encoding="utf-8", errors="replace")
            match = FRONT_MATTER.match(raw)
            front = match.group(1) if match else ""
            body = SHORTCODE.sub("", raw[match.end():] if match else raw)
            rel = path.relative_to(root).with_suffix("")
            url = "https://kubernetes.io/docs/" + str(rel).removesuffix("_index").rstrip("/") + "/"
            title = title_of(front, path)
            for i, text in enumerate(chunks(body)):
                chunk_id = hashlib.sha1(f"{rel}#{i}".encode()).hexdigest()[:16]
                f.write(json.dumps({"id": chunk_id, "title": title, "url": url, "text": text}) + "\n")
                count += 1
    print(f"wrote {count} chunks to {out}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
