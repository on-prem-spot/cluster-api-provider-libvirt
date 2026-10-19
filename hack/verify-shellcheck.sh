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

# verify-shellcheck.sh runs shellcheck over every tracked *.sh file.
# If shellcheck is not installed the check is skipped (exit 0) so that
# `make verify` still works on machines without it; CI installs it.
#
# Set SHELLCHECK to point at a specific binary.

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

SHELLCHECK="${SHELLCHECK:-shellcheck}"

if ! command -v "${SHELLCHECK}" >/dev/null 2>&1; then
  echo "shellcheck not found; skipping shell script checks."
  echo "Install it from https://github.com/koalaman/shellcheck#installing to enable them."
  exit 0
fi

files=()
while IFS= read -r -d '' file; do
  [[ -f "${file}" ]] && files+=("${file}")
done < <(git ls-files -z -- '*.sh' ':(exclude,glob)**/vendor/**')

if [[ ${#files[@]} -eq 0 ]]; then
  echo "No shell scripts to check."
  exit 0
fi

"${SHELLCHECK}" --version | sed -n 's/^version: /Using shellcheck /p'
"${SHELLCHECK}" --external-sources "${files[@]}"
echo "shellcheck passed for ${#files[@]} files."
