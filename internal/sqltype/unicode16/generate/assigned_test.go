package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func assignedTestRecord(code, name, category string) string {
	fields := make([]string, 15)
	fields[0], fields[1], fields[2], fields[3], fields[4], fields[9] = code, name, category, "0", "L", "N"
	return strings.Join(fields, ";") + "\n"
}

func TestGenerateAssignedRanges(t *testing.T) {
	dir := t.TempDir()
	data := assignedTestRecord("0041", "A", "Lu") + assignedTestRecord("0042", "B", "Lu") + assignedTestRecord("0043", "UNASSIGNED", "Cn") +
		assignedTestRecord("0044", "D", "Lu") + assignedTestRecord("0100", "<TEST, First>", "Lo") + assignedTestRecord("010F", "<TEST, Last>", "Lo") +
		assignedTestRecord("0110", "NEXT", "Lo") + assignedTestRecord("E000", "<PRIVATE, First>", "Co") + assignedTestRecord("F8FF", "<PRIVATE, Last>", "Co") +
		assignedTestRecord("FFFF", "UNASSIGNED", "Cn")
	if err := os.WriteFile(filepath.Join(dir, "UnicodeData.txt"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := assignedData(dir)
	want := []interval{{lo: 0x41, hi: 0x42}, {lo: 0x44, hi: 0x44}, {lo: 0x100, hi: 0x110}, {lo: 0xe000, hi: 0xf8ff}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err, want)
	}
	generated, err := generateAssigned(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated, []byte("{0x100, 0x110}")) || bytes.Contains(generated, []byte("0xFFFF")) {
		t.Fatal(string(generated))
	}
}

func TestGenerateAssignedValidation(t *testing.T) {
	first := assignedTestRecord("0100", "<TEST, First>", "Lo")
	for _, data := range []string{
		"0041;bad\n", assignedTestRecord("ZZZZ", "A", "Lu"), assignedTestRecord("110000", "A", "Lu"), assignedTestRecord("0041", "A", "XX"),
		first, assignedTestRecord("010F", "<TEST, Last>", "Lo"), first + assignedTestRecord("010F", "<OTHER, Last>", "Lo"),
		first + assignedTestRecord("010F", "<TEST, Last>", "Co"), first + assignedTestRecord("0101", "<NESTED, First>", "Lo"),
		first + assignedTestRecord("0101", "A", "Lo"), first + assignedTestRecord("00FF", "<TEST, Last>", "Lo"),
		assignedTestRecord("0042", "B", "Lu") + assignedTestRecord("0041", "A", "Lu"), assignedTestRecord("0041", "A", "Lu") + assignedTestRecord("0041", "B", "Lu"),
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "UnicodeData.txt"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := generateAssigned(dir); err == nil {
			t.Fatal("accepted malformed assigned input", data)
		}
	}
}

func TestGenerateAssignedReproducible(t *testing.T) {
	first, err := generateAssigned("../data")
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateAssigned("../data")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../assigned_tables_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, committed) {
		t.Fatal("assigned regeneration is not byte-identical")
	}
}
