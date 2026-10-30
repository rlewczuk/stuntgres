package reference_test

import (
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independent REL_18_6 getAffixFlagSet expectations: AF 2 reserves total 3
// immediately, although only slot 1 exists when the interleaved rule is read.
// These source-grounded expectations must be observed on the pinned oracle.
func ispellInterleavedAliasCases() []ispellNumericCase {
	var cases []ispellNumericCase
	for _, boundary := range []struct {
		name, raw, message string
		compound, continuation bool
	}{
		{"uninitialized", "2", `invalid affix alias "2"`, false, false},
		{"fixed_total_empty", "3", "", false, false},
		{"beyond_total", "4", `invalid affix alias "4"`, false, false},
		{"initialized", "1", "", false, false},
		{"zero_empty", "0", "", false, false},
		{"negative_empty", "-1", "", false, false},
		{"wrapped_uninitialized", "4294967298", `invalid affix alias "4294967298"`, false, false},
		{"wrapped_fixed_total_empty", "4294967299", "", false, false},
		{"wrapped_beyond_total", "4294967300", `invalid affix alias "4294967300"`, false, false},
		{"wrapped_original_tail", "+4294967298tail", `invalid affix alias "+4294967298tail"`, false, false},
		{"initialized_tail", "1tail", "", false, false},
		{"negative_wrapped_uninitialized", "-4294967294", `invalid affix alias "-4294967294"`, false, false},
		{"long_overflow", "9223372036854775808", `invalid affix alias "9223372036854775808"`, false, false},
		{"compound_uninitialized", "2", `invalid affix alias "2"`, true, false},
		{"compound_fixed_total_empty", "3", "", true, false},
		{"compound_initialized", "1", "", true, true},
		{"compound_zero_empty", "0", "", true, false},
		{"compound_negative_empty", "-1", "", true, false},
		{"compound_wrapped_initialized", "+4294967297tail", "", true, true},
		{"compound_wrapped_fixed_total_empty", "4294967299", "", true, false},
	} {
		prefix := "FLAG num\n"
		line := 5
		if boundary.compound { prefix += "COMPOUNDFLAG 1\n"; line++ }
		rule := "SFX 1 0 s/" + boundary.raw + " .\n"
		tc := ispellNumericCase{
			name: boundary.name, dictionary: "root/1\n",
			affix: prefix + "AF 2\nAF 1\nSFX 1 N 1\n" + rule + "AF 1\n",
			message: boundary.message, compound: boundary.compound,
			roots: "{root}", configuration: true,
		}
		if boundary.message != "" { tc.line, tc.readerText = line, rule }
		if boundary.continuation { tc.rootsroot = "{root,root}" }
		cases = append(cases, tc)
	}
	cases = append(cases,
		ispellNumericCase{name: "completed_slot_two", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 2\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/2 .\n", roots: "{root}", compound: true, rootsroot: "{root,root}", configuration: true},
		ispellNumericCase{name: "initialized_empty_incomplete", dictionary: "root/2\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 2\nAF\nSFX 1 N 1\nSFX 1 0 s/1 .\nAF 1\n", roots: "{root}", compound: true, configuration: true},
		ispellNumericCase{name: "initialized_empty_completed", dictionary: "root/1\n", affix: "FLAG num\nCOMPOUNDFLAG 1\nAF 2\nAF 1\nAF\nSFX 1 N 1\nSFX 1 0 s/2 .\n", roots: "{root}", compound: true, configuration: true},
		ispellNumericCase{name: "missing_final_entry", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s/3 .\n", message: "number of aliases is less than specified number 2", configuration: true},
		ispellNumericCase{name: "extra_final_entry", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s/3 .\nAF 1\nAF 1\n", message: "number of aliases exceeds specified number 2", line: 7, readerText: "AF 1\n", configuration: true},
		ispellNumericCase{name: "earlier_alias_before_missing_count", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s/2 .\n", message: `invalid affix alias "2"`, line: 5, readerText: "SFX 1 0 s/2 .\n", configuration: true},
		ispellNumericCase{name: "empty_continuation", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s/ .\nAF 1\n", roots: "{root}", configuration: true},
		ispellNumericCase{name: "no_continuation", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s .\nAF 1\n", roots: "{root}", configuration: true},
		ispellNumericCase{name: "completed_fixed_total", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/3 .\n", roots: "{root}", configuration: true},
		ispellNumericCase{name: "completed_beyond_total", dictionary: "root/1\n", affix: "FLAG num\nAF 2\nAF 1\nAF 1\nSFX 1 N 1\nSFX 1 0 s/4 .\n", message: `invalid affix alias "4"`, line: 6, readerText: "SFX 1 0 s/4 .\n", configuration: true},
	)
	return cases
}

// Explicit package-unique resources are installed unchanged in both engines.
// No host filesystem lookup, generated oracle expectation or sample fallback.
func ispellInterleavedAliasFiles() []stuntgres.TextSearchFile {
	var files []stuntgres.TextSearchFile
	for _, tc := range ispellInterleavedAliasCases() {
		name := "tr_ispell_interleaved_" + tc.name
		files = append(files,
			stuntgres.TextSearchFile{Name: name + ".dict", Data: []byte(tc.dictionary)},
			stuntgres.TextSearchFile{Name: name + ".affix", Data: []byte(tc.affix)})
	}
	return files
}

func TestReferenceIspellInterleavedAliases(t *testing.T) {
	for _, tc := range ispellInterleavedAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
					p := fixture.NewScenarioWithDriverConfig(t, ispellConfigure)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							ispellRunNumericCase(t, p, b, tc, prepared, "tr_ispell_interleaved_"+tc.name)
						})
					}
				})
			}
		})
	}
}
