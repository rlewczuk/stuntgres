package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

// Exercise the actual compiler's reader phase and published morphology. Bounds
// are declared-table bounds, not the current appended prefix or string content.
func TestIspellInterleavedAliases(t *testing.T) {
	for _, tc := range []struct {
		name, raw, message string
		compound, continuation bool
	}{
		{"uninitialized_without_compound", "2", `invalid affix alias "2"`, false, false},
		{"fixed_total_without_compound", "3", "", false, false},
		{"beyond_total_without_compound", "4", `invalid affix alias "4"`, false, false},
		{"initialized_without_compound", "1", "", false, false},
		{"uninitialized_with_compound", "2", `invalid affix alias "2"`, true, false},
		{"fixed_total_with_compound", "3", "", true, false},
		{"initialized_with_compound", "1", "", true, true},
		{"zero_with_compound", "0", "", true, false},
		{"negative_with_compound", "-1", "", true, false},
		{"wrapped_uninitialized", "+4294967298tail", `invalid affix alias "+4294967298tail"`, false, false},
		{"wrapped_total", "4294967299", "", true, false},
		{"wrapped_initialized", "4294967297tail", "", true, true},
		{"overflow", "9223372036854775808", `invalid affix alias "9223372036854775808"`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, line := "FLAG num\n", "5"
			if tc.compound { prefix += "COMPOUNDFLAG 1\n"; line = "6" }
			rule := "SFX 1 0 s/" + tc.raw + " .\n"
			compiler := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict": "root/1\n",
				"rules.affix": prefix + "AF 2\nAF 1\nSFX 1 N 1\n" + rule + "AF 1\n",
			}))
			if err := compiler.LoadDictionary(context.Background(), "words"); err != nil { t.Fatal(err) }
			err := compiler.LoadAffixes(context.Background(), "rules")
			if tc.message != "" {
				var diagnostic *sqlstate.Error
				wantContext := "line " + line + " of configuration file \"rules.affix\": \"" + rule + "\""
				if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != tc.message || diagnostic.Context != wantContext { t.Fatalf("reader: %#v err=%v", diagnostic, err) }
				return
			}
			if err != nil { t.Fatal(err) }
			d, err := compiler.Finish(context.Background())
			if err != nil { t.Fatal(err) }
			for range 2 {
				ispellAssert(t, d, "root", []string{"root"})
				ispellAssert(t, d, "roots", []string{"root"})
				ispellAssert(t, d, "unknown", nil)
				var compound, continuation []string
				if tc.compound { compound = []string{"root", "root"} }
				if tc.continuation { continuation = []string{"root", "root"} }
				ispellAssert(t, d, "rootroot", compound)
				ispellAssert(t, d, "rootsroot", continuation)
			}
		})
	}
	for _, tc := range []struct { name, dictionary, entries, tail string; continuation bool }{
		{"completed_slot_two", "root/1\n", "AF 1\nAF 1\n", "", true},
		{"completed_empty_slot_two", "root/1\n", "AF 1\nAF\n", "", false},
		{"incomplete_empty_slot_one", "root/2\n", "AF\n", "AF 1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alias := "2"
			if tc.tail != "" { alias = "1" }
			d := ispellCompile(t, tc.dictionary, "FLAG num\nCOMPOUNDFLAG 1\nAF 2\n" + tc.entries + "SFX 1 N 1\nSFX 1 0 s/" + alias + " .\n" + tc.tail, "")
			for range 2 {
				ispellAssert(t, d, "root", []string{"root"})
				ispellAssert(t, d, "roots", []string{"root"})
				ispellAssert(t, d, "rootroot", []string{"root", "root"})
				var continuation []string
				if tc.continuation { continuation = []string{"root", "root"} }
				ispellAssert(t, d, "rootsroot", continuation)
			}
		})
	}
	for _, tc := range []struct{ name, dictionary, message string; suffix bool }{
		{"dictionary_later_slot", "root/2\n", "", true},
		{"dictionary_fixed_total_rejected", "root/3\n", `invalid affix alias "3"`, false},
		{"dictionary_negative_rejected", "root/-1\n", `invalid affix alias "-1"`, false},
		{"dictionary_tail_rejected", "root/1tail\n", `invalid affix alias "1tail"`, false},
		{"dictionary_zero_slot", "root/0\n", "", false},
		{"dictionary_empty_slot", "root/\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compiler := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict": tc.dictionary,
				"rules.affix": "FLAG num\nAF 2\nAF 1\nSFX 1 N 1\nSFX 1 0 s/1 .\nAF 1\n",
			}))
			// A later dictionary slot is checked only after import completes,
			// not against the populated prefix when dictionary bytes are read.
			if err := compiler.LoadDictionary(context.Background(), "words"); err != nil { t.Fatal(err) }
			if err := compiler.LoadAffixes(context.Background(), "rules"); err != nil { t.Fatal(err) }
			d, err := compiler.Finish(context.Background())
			if tc.message != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != tc.message || diagnostic.Context != "" { t.Fatalf("Finish: %#v err=%v", diagnostic, err) }
				return
			}
			if err != nil { t.Fatal(err) }
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "unknown", nil)
			var suffix []string
			if tc.suffix { suffix = []string{"root"} }
			ispellAssert(t, d, "roots", suffix)
		})
	}
}
