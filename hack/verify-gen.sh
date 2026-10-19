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

# verify-gen.sh regenerates CRDs, RBAC, webhook manifests and DeepCopy code
# (`make manifests generate`) and fails if that changes the working tree,
# i.e. if checked-in generated files are stale.
#
# The working tree is compared before and after generation rather than
# against HEAD, so uncommitted edits that are already up to date pass and
# the script can be run locally mid-change. In CI the tree starts clean, so
# this is equivalent to `git diff --exit-code` after generating.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

MAKE="${MAKE:-make}"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# snapshot prints "<blob hash> <path>" for every file that differs from HEAD
# (modified, deleted or untracked), sorted by path.
snapshot() {
  local path
  {
    git diff --name-only -z HEAD
    git ls-files -z --others --exclude-standard
  } | sort -zu | while IFS= read -r -d '' path; do
    if [[ -e "${path}" ]]; then
      printf '%s %s\n' "$(git hash-object -- "${path}")" "${path}"
    else
      printf 'deleted %s\n' "${path}"
    fi
  done
}

snapshot >"${tmp}/before"
"${MAKE}" manifests generate
snapshot >"${tmp}/after"

if ! cmp -s "${tmp}/before" "${tmp}/after"; then
  echo >&2
  echo "Generated files are out of date. Run 'make manifests generate' and commit the result." >&2
  echo "Files changed by generation:" >&2
  diff "${tmp}/before" "${tmp}/after" | sed -n -E 's/^[<>] [^ ]+ /  /p' | sort -u >&2
  exit 1
fi

echo "Generated files are up to date."
