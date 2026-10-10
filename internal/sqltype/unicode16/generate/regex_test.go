package main

import (
	"bytes"
	"os"
	"testing"
)

func TestUnicode16RegexGeneration(t *testing.T) {
	first, err := generateRegex("../data")
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateRegex("../data")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../regex_tables_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, committed) {
		t.Fatal("regex regeneration is not byte-identical")
	}
}
