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

# verify-boilerplate.sh checks that every tracked Go and shell source file
# carries the Apache 2.0 license header from hack/boilerplate.go.txt.
#
# The check is deliberately loose: the copyright year and holder are not
# compared, only that a "Copyright" line and the key Apache license lines
# appear near the top of the file (after any shebang or build constraints).
# Generated files (zz_generated*) and vendored code are skipped.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# How many leading lines of each file to search for the header.
HEADER_LINES=25

# Lines that must all be present in the header. Comment markers ("#", "//",
# "/*", "*") are stripped before matching, so one list works for Go and shell.
REQUIRED_PATTERNS=(
  '^Copyright [0-9]{4}'
  '^Licensed under the Apache License, Version 2\.0 \(the "License"\);$'
  '^http://www\.apache\.org/licenses/LICENSE-2\.0$'
  '^limitations under the License\.$'
)

check_file() {
  local file="$1"
  local header pattern
  header="$(head -n "${HEADER_LINES}" "${file}" |
    sed -E -e 's@^[[:space:]]*(//|#|/\*|\*)?[[:space:]]*@@' -e 's@[[:space:]]+$@@')"
  for pattern in "${REQUIRED_PATTERNS[@]}"; do
    if ! grep -Eq -- "${pattern}" <<<"${header}"; then
      return 1
    fi
  done
}

failed=()
while IFS= read -r -d '' file; do
  # Deleted-but-unstaged files are still listed by git ls-files.
  [[ -f "${file}" ]] || continue
  if ! check_file "${file}"; then
    failed+=("${file}")
  fi
done < <(git ls-files -z -- '*.go' '*.sh' \
  ':(exclude,glob)**/vendor/**' \
  ':(exclude,glob)**/zz_generated*')

if [[ ${#failed[@]} -gt 0 ]]; then
  echo "The following files are missing the Apache 2.0 license header" >&2
  echo "(see hack/boilerplate.go.txt):" >&2
  printf '  %s\n' "${failed[@]}" >&2
  exit 1
fi

echo "All Go and shell files carry the license header."
