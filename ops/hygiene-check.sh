#!/usr/bin/env bash
# hygiene-check fails, naming each path, when the tracked tree contains
# build-loop critic/mutation evidence: a directory named critic-<hex8>, a file
# named mut-<n>-*.txt, or make-check.txt outside testdata/. Those are loop
# artifacts, not product. Runs against the git repo in the current directory.
set -euo pipefail

bad=$(git ls-files -z | tr '\0' '\n' | grep -E \
	'(^|/)critic-[0-9a-f]{8}(/|$)|(^|/)mut-[0-9]+-[^/]*\.txt$|^(.*/)?make-check\.txt$' |
	grep -Ev '(^|/)testdata/' || true)

if [[ -n "$bad" ]]; then
	echo "hygiene-check: committed critic/mutation evidence must not be tracked:" >&2
	while IFS= read -r p; do echo "  $p" >&2; done <<<"$bad"
	exit 1
fi
