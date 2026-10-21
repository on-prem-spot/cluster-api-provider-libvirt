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

"""The demo's use of PostgreSQL + pgvector, reached through the HA virtual IP."""

import os

import psycopg

MODEL = os.environ.get("MODEL", "BAAI/bge-base-en-v1.5")
DIMENSIONS = int(os.environ.get("DIMENSIONS", "768"))

SCHEMA = f"""
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS chunks (
    id          text PRIMARY KEY,
    title       text NOT NULL,
    url         text NOT NULL,
    content     text NOT NULL,
    embedding   vector({DIMENSIONS}) NOT NULL,
    node        text NOT NULL,
    pod         text NOT NULL,
    shard       int  NOT NULL,
    embedded_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chunks_embedded_at ON chunks (embedded_at);
CREATE TABLE IF NOT EXISTS heartbeat (
    id      bigserial PRIMARY KEY,
    at      timestamptz NOT NULL DEFAULT now(),
    server  inet
);
"""


def connect(timeout: int = 3) -> psycopg.Connection:
    return psycopg.connect(
        host=os.environ.get("PGHOST", "10.0.1.250"),
        port=int(os.environ.get("PGPORT", "5432")),
        dbname=os.environ.get("PGDATABASE", "demo"),
        user=os.environ.get("PGUSER", "demo"),
        password=os.environ["PGPASSWORD"],
        connect_timeout=timeout,
        # A powered-off primary sends no reset: without these, a write to it
        # hangs until the kernel gives up on the connection, long after the
        # virtual IP has moved.
        tcp_user_timeout=3000,
        keepalives=1,
        keepalives_idle=2,
        keepalives_interval=1,
        keepalives_count=2,
        options="-c statement_timeout=10000",
        autocommit=False,
    )


def init(conn: psycopg.Connection) -> None:
    with conn.cursor() as cur:
        cur.execute(SCHEMA)
    conn.commit()


def stored_ids(conn: psycopg.Connection, ids: list[str]) -> set[str]:
    with conn.cursor() as cur:
        cur.execute("SELECT id FROM chunks WHERE id = ANY(%s)", (ids,))
        return {row[0] for row in cur.fetchall()}


def store(conn, batch, vectors, node: str, pod: str, shard: int) -> None:
    with conn.cursor() as cur:
        cur.executemany(
            "INSERT INTO chunks (id, title, url, content, embedding, node, pod, shard) "
            "VALUES (%s, %s, %s, %s, %s::vector, %s, %s, %s) ON CONFLICT (id) DO NOTHING",
            [
                (i["id"], i["title"], i["url"], i["text"], "[" + ",".join(f"{x:.6f}" for x in v) + "]",
                 node, pod, shard)
                for i, v in zip(batch, vectors)
            ],
        )
    conn.commit()
