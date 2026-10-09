package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnicode16CaseGeneration(t *testing.T) {
	first, err := generateCase("../data")
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateCase("../data")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../case_tables_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(first, committed) {
		t.Fatal("case regeneration is not byte-identical")
	}
}
func TestUnicode16CaseGeneratorValidation(t *testing.T) {
	for _, bad := range []string{"", "110000", "D800", "0061 0062 0063 0064", "ZZZZ"} {
		if _, err := caseSequence(bad); err == nil {
			t.Fatal("accepted case expansion", bad)
		}
	}
	for _, bad := range []string{"110000", "0020..0010", "0000..0010..0020", "ZZZZ"} {
		if _, err := caseBounds(bad); err == nil {
			t.Fatal("accepted property range", bad)
		}
	}
	got := mergeCaseRanges([]interval{{lo: 9, hi: 12}, {lo: 1, hi: 3}, {lo: 3, hi: 4}, {lo: 5, hi: 7}, {lo: 10, hi: 14}})
	if !reflect.DeepEqual(got, []interval{{lo: 1, hi: 7}, {lo: 9, hi: 14}}) {
		t.Fatal("deterministic range merge", got)
	}
	for _, tc := range []struct{ file, bad string }{
		{"UnicodeData.txt", "0041;bad"},
		{"UnicodeData.txt", "0030;<TEST, Last>;Nd;0;EN;;;;;N;;;;;"},
		{"SpecialCasing.txt", "03A3; 03C2; 03A3; 03A3; Final_Sigma;"}, // duplicate condition
		{"SpecialCasing.txt", "0041; 0061 0062 0063 0064; 0041; 0041; ;"},
		{"CaseFolding.txt", "0041; X; 0061;"},
		{"CaseFolding.txt", "0041; S; 0061 0062;"},
		{"DerivedCoreProperties.txt", "0020..0010 ; Alphabetic"},
	} {
		t.Run(tc.file+tc.bad, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"UnicodeData.txt", "SpecialCasing.txt", "CaseFolding.txt", "DerivedCoreProperties.txt"} {
				data, err := os.ReadFile(filepath.Join("../data", name))
				if err != nil {
					t.Fatal(err)
				}
				if name == tc.file {
					data = append(bytes.TrimRight(data, "\n"), []byte("\n"+tc.bad+"\n")...)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := generateCase(dir); err == nil {
				t.Fatal("accepted malformed relevant record")
			}
		})
	}
}
