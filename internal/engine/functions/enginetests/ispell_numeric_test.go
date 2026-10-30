package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

// Supplement the public direct/prepared oracle scenario with actual compiler
// phase boundaries and owned deferred diagnostics, without SQL or test hooks.
func TestIspellNumericConversion(t *testing.T) {
	for _, tc := range []struct{ raw, message string; recognized bool }{
		{"2147483648", `affix flag "2147483648" is out of range`, false},
		{"2147483647", `affix flag "2147483647" is out of range`, false},
		{"-2147483648", `affix flag "-2147483648" is out of range`, false},
		{"-2147483649", `affix flag "-2147483649" is out of range`, false},
		{"4294967295", `affix flag "4294967295" is out of range`, false},
		{"4294967296", "", false},
		{"-4294967296", "", false},
		{"4295032832", "", false},
		{"4295032833", `affix flag "4295032833" is out of range`, false},
		{"4294967297", "", true},
		{"-4294967295", "", true},
		{"9223372036854775807", `affix flag "9223372036854775807" is out of range`, false},
		{"-9223372036854775808", "", false},
		{"9223372036854775808", `invalid affix flag "9223372036854775808"`, false},
		{"-9223372036854775809", `invalid affix flag "-9223372036854775809"`, false},
		{"-1", `affix flag "-1" is out of range`, false},
		{"65536", "", false},
		{"65537", `affix flag "65537" is out of range`, false},
		{"0", "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			line := "COMPOUNDFLAG " + tc.raw + "\n"
			compiler := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict": "root/1\n", "rules.affix": "FLAG num\n" + line,
			}))
			if err := compiler.LoadDictionary(context.Background(), "words"); err != nil { t.Fatal(err) }
			err := compiler.LoadAffixes(context.Background(), "rules")
			if tc.message != "" {
				var diagnostic *sqlstate.Error
				// Reader Context includes the original literal newline, not an
				// escaped Go representation of that newline.
				wantContext := "line 2 of configuration file \"rules.affix\": \"" + line + "\""
				if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != tc.message || diagnostic.Context != wantContext { t.Fatalf("eager: %#v err=%v", diagnostic, err) }
			} else {
				if err != nil { t.Fatal(err) }
				d, err := compiler.Finish(context.Background())
				if err != nil { t.Fatal(err) }
				ispellAssert(t, d, "root", []string{"root"})
				var compound []string
				if tc.recognized { compound = []string{"root", "root"} }
				ispellAssert(t, d, "rootroot", compound)
			}

			// No compound flag: raw dictionary vectors survive Finish. A base
			// lookup bypasses membership, suffix demand reaches the decoder.
			d := ispellCompile(t, "root/" + tc.raw + "\n", "FLAG num\nSFX 1 N 1\nSFX 1 0 s .\n", "")
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "unknown", nil)
			for range 2 {
				if tc.message == "" {
					var want []string
					if tc.recognized { want = []string{"root"} }
					ispellAssert(t, d, "roots", want)
				} else {
					_, err := d.Lexize(context.Background(), "roots")
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != tc.message || diagnostic.Context != "" { t.Fatalf("deferred: %#v err=%v", diagnostic, err) }
					diagnostic.Message = "caller-owned mutation"
				}
				ispellAssert(t, d, "root", []string{"root"})
			}
		})
	}
	t.Run("prefix_before_invalid_tail", func(t *testing.T) {
		d := ispellCompile(t, "root/4294967297,2147483648\n", "FLAG num\nPFX 1 N 1\nPFX 1 0 pre .\nPFX 2 N 1\nPFX 2 0 un .\n", "")
		for range 2 {
			ispellAssert(t, d, "preroot", []string{"root"})
			_, err := d.Lexize(context.Background(), "unroot")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `affix flag "2147483648" is out of range` || diagnostic.Context != "" { t.Fatalf("exhausted membership: %#v err=%v", diagnostic, err) }
			diagnostic.Message = "caller-owned mutation"
			ispellAssert(t, d, "preroot", []string{"root"})
		}
	})
	for _, command := range []string{"COMPOUNDFLAG", "COMPOUNDBEGIN", "COMPOUNDMIDDLE", "COMPOUNDLAST", "COMPOUNDEND", "ONLYINCOMPOUND", "COMPOUNDPERMITFLAG", "COMPOUNDFORBIDFLAG"} {
		t.Run("directive_"+command, func(t *testing.T) {
			line := command + " 2147483648\n"
			b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"rules.affix": "FLAG num\n" + line}))
			err := b.LoadAffixes(context.Background(), "rules")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != `affix flag "2147483648" is out of range` || diagnostic.Context != "line 2 of configuration file \"rules.affix\": \"" + line + "\"" { t.Fatalf("directive boundary: %#v err=%v", diagnostic, err) }
		})
	}
}
