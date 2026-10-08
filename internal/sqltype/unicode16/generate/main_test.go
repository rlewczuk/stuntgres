package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUnicode16Generation(t *testing.T) {
	first, err := generate("../data")
	if err != nil {
		t.Fatal(err)
	}
	second, err := generate(filepath.Join("..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../tables_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, committed) {
		t.Fatal("Unicode16 regeneration is not byte-identical")
	}
	if !bytes.Contains(first, []byte(`const Version = "16.0.0"`)) {
		t.Fatal("missing pinned Unicode version")
	}
}
