.PHONY: generate test reference smoke bench

generate:
	go generate ./internal/parser

test:
	go test ./...

reference:
	go test -tags reference -run TestReference -count=1 -v ./reference

smoke:
	go test -count=1 -v ./smoke

bench:
	go test -run '^$$' -bench '^BenchmarkOpenDatabase$$' -benchmem -count=5 .
