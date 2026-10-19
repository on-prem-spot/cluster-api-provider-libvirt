#!/usr/bin/env bash
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

# verify-modules.sh runs `go mod tidy` and fails if it changes go.mod or
# go.sum. Like verify-gen.sh it compares the files before and after tidying,
# so uncommitted but already-tidy module edits pass.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

cp go.mod "${tmp}/go.mod"
cp go.sum "${tmp}/go.sum"

go mod tidy

status=0
for f in go.mod go.sum; do
  if ! diff -u "${tmp}/${f}" "${f}" >&2; then
    status=1
  fi
done

if [[ ${status} -ne 0 ]]; then
  echo >&2
  echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result." >&2
  exit 1
fi

echo "go.mod and go.sum are tidy."
