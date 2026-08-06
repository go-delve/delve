#!/usr/bin/env bash
# Run FuzzEvalExpression setup, then either the seed corpus (seed) or a
# timed continuous fuzz (fuzz). Intended for CI; linux/amd64 only.
set -euo pipefail

mode="${1:-}"
if [[ "$mode" != "seed" && "$mode" != "fuzz" ]]; then
	echo "usage: $0 <seed|fuzz>" >&2
	exit 2
fi

if [[ "$(uname -s)" != "Linux" || "$(uname -m)" != "x86_64" ]]; then
	echo "fuzz_eval_expression: skipping (requires linux/amd64, got $(uname -s)/$(uname -m))"
	exit 0
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

echo "fuzz_eval_expression: setup"
go test ./pkg/proc -count=1 -run=FuzzEvalExpression -fuzzevalexpressionsetup

case "$mode" in
seed)
	echo "fuzz_eval_expression: seed"
	go test ./pkg/proc -count=1 -run=FuzzEvalExpression
	;;
fuzz)
	echo "fuzz_eval_expression: fuzz (2m)"
	go test ./pkg/proc -count=1 -run=NONE -fuzz=FuzzEvalExpression \
		-fuzztime=2m -fuzzminimizetime=0 -v
	;;
esac
