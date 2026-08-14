#!/usr/bin/env bash
# Run FuzzEvalExpression setup, then either the seed corpus (seed) or a
# timed continuous fuzz (fuzz). Fuzz mode also runs 5s continuous smokes for
# FuzzEvalStackOps and FuzzCallInjectionProtocol. Intended for CI;
# linux/amd64 only.
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

	echo "fuzz_eval_expression: FuzzEvalStackOps (5s)"
	go test ./pkg/proc -count=1 -run=NONE -fuzz=FuzzEvalStackOps \
		-fuzztime=5s -fuzzminimizetime=0

	echo "fuzz_eval_expression: FuzzCallInjectionProtocol (5s)"
	go test ./pkg/proc -count=1 -run=NONE -fuzz=FuzzCallInjectionProtocol \
		-fuzztime=5s -fuzzminimizetime=0
	;;
esac
