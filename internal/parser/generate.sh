#!/bin/sh
set -eu

# go generate runs here; also resolve the package directory for direct invocation.
cd "$(dirname "$0")"
tmp=$(mktemp -d .grammar-gen.XXXXXX)
trap 'rm -rf "$tmp"' 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Keep the original input/output names (including the generated header) while
# isolating concurrent writers on the destination filesystem.
cp grammar.y "$tmp/grammar.y"
(
    cd "$tmp"
    go run golang.org/x/tools/cmd/goyacc@v0.50.0 -l -p sql -o grammar_gen.go -v /dev/null grammar.y
)
# A failed generator exits before this same-filesystem atomic rename.
mv -f "$tmp/grammar_gen.go" grammar_gen.go
