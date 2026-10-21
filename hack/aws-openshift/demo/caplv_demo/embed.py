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

"""One shard of the embedding run: an index of a Kueue-managed Indexed Job.

Each pod takes the chunks whose id hashes to its index, skips the ones
already in pgvector, and embeds the rest in batches. Because it skips what
is stored, a pod killed with its worker VM, or one that loses the database
during a failover, picks up where it left off when it runs again.
"""

import gzip
import json
import os
import socket
import sys
import time
import zlib

import psycopg

from caplv_demo import db

CORPUS = os.environ.get("CORPUS", "/opt/corpus/k8s-docs.jsonl.gz")
BATCH = int(os.environ.get("BATCH", "16"))


def log(message: str) -> None:
    print(f"{time.strftime('%H:%M:%S')} {message}", flush=True)


def shard_items(index: int, count: int):
    with gzip.open(CORPUS, "rt", encoding="utf-8") as f:
        for line in f:
            item = json.loads(line)
            if zlib.crc32(item["id"].encode()) % count == index:
                yield item


def main() -> None:
    index = int(os.environ["JOB_COMPLETION_INDEX"])
    count = int(os.environ.get("SHARDS", sys.argv[1] if len(sys.argv) > 1 else "1"))
    node = os.environ.get("NODE_NAME", socket.gethostname())
    pod = os.environ.get("POD_NAME", socket.gethostname())
    items = list(shard_items(index, count))
    log(f"shard {index}/{count} on {node}: {len(items)} chunks")

    # The model loads once; the database may come and go.
    from fastembed import TextEmbedding

    model = TextEmbedding(db.MODEL, cache_dir=os.environ.get("FASTEMBED_CACHE_PATH"), local_files_only=True)

    done = 0
    while True:
        try:
            with db.connect() as conn:
                stored = db.stored_ids(conn, [i["id"] for i in items])
                todo = [i for i in items if i["id"] not in stored]
                if done == 0:
                    log(f"{len(stored)} already stored, {len(todo)} to embed")
                for start in range(0, len(todo), BATCH):
                    batch = todo[start:start + BATCH]
                    vectors = list(model.embed([f"{i['title']}\n{i['text']}" for i in batch]))
                    db.store(conn, batch, vectors, node, pod, index)
                    done += len(batch)
                log(f"shard {index} complete: {done} embedded by this pod")
                return
        except psycopg.OperationalError as e:
            # Most likely a failover in progress: the virtual IP is moving.
            log(f"database unavailable ({e.__class__.__name__}); retrying in 2s")
            time.sleep(2)


if __name__ == "__main__":
    main()
