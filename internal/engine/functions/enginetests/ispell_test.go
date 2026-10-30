package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func ispellCompile(t *testing.T, dict, affix, stop string) *functions.IspellDictionary {
	t.Helper()
	b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"words.dict": dict, "rules.affix": affix, "removed.stop": stop}))
	for _, load := range []func() error{
		func() error { return b.LoadDictionary(context.Background(), "words") },
		func() error { return b.LoadAffixes(context.Background(), "rules") },
		func() error { return b.LoadStopwords(context.Background(), "removed") },
	} {
		if err := load(); err != nil {
			t.Fatal(err)
		}
	}
	d, err := b.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func ispellAssert(t *testing.T, d *functions.IspellDictionary, token string, want []string) {
	t.Helper()
	got, err := d.Lexize(context.Background(), token)
	if err != nil || got.Recognized != (want != nil) {
		t.Fatalf("%q: lexical result=%v err=%v", token, got, err)
	}
	if len(got.Lexemes) != len(want) {
		t.Fatalf("%q forms=%v want=%v", token, got.Lexemes, want)
	}
	for i, text := range want {
		if got.Lexemes[i].Text != text || got.Lexemes[i].Prefix {
			t.Fatalf("%q[%d]=%v want=%q", token, i, got.Lexemes[i], text)
		}
	}
}
func TestIspellDictionaryParsing(t *testing.T) {
	d := ispellCompile(t, "4\nMiXeD/A\nwrong/a\nROOT/A trailing\nplain ignored\nslash/AB/ignored\n", "SFX A Y 1\nSFX A 0 s .\n", "")
	for _, tc := range []struct {
		token string
		want  []string
	}{
		{"4", []string{"4"}}, {"MIXEDS", []string{"mixed"}}, {"wrongs", nil},
		{"roots", []string{"root"}}, {"plain", []string{"plain"}}, {"plain ignored", nil}, {"slashs", []string{"slash"}},
		{"", nil}, {" root", nil}, {"root!", nil},
	} {
		ispellAssert(t, d, tc.token, tc.want)
	}
	// Duplicate spellings combine actual flag sets, not only the last record.
	d = ispellCompile(t, "stem/A\nstem/B\n", "SFX A Y 1\nSFX A 0 s .\nSFX B Y 1\nSFX B 0 ed .\n", "")
	ispellAssert(t, d, "stems", []string{"stem"})
	ispellAssert(t, d, "stemed", []string{"stem"})
	// Distinct raw long-flag identities merge before decoding. Both suffix
	// keys recognize the result, without assuming equal-word record order.
	for _, tc := range []struct{ name, dictionary string }{
		{"forward", "root/A\nroot/B\n"},
		{"reverse", "root/B\nroot/A\n"},
	} {
		t.Run("distinct_raw_long_"+tc.name, func(t *testing.T) {
			d := ispellCompile(t, tc.dictionary, "FLAG long\nSFX AB N 1\nSFX AB 0 s .\nSFX BA N 1\nSFX BA 0 s .\n", "")
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "roots", []string{"root"})
			ispellAssert(t, d, "unknown", nil)
		})
	}
	t.Run("equal_raw_long_identity", func(t *testing.T) {
		// Equal identities are not concatenated to AA. Without compound
		// demand, initialization and base recognition still succeed.
		d := ispellCompile(t, "root/A\nroot/A\n", "FLAG long\nSFX AA N 1\nSFX AA 0 s .\n", "")
		ispellAssert(t, d, "root", []string{"root"})
		ispellAssert(t, d, "unknown", nil)
		for range 2 {
			_, err := d.Lexize(context.Background(), "roots")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
				diagnostic.Message != `invalid affix flag "A" with "long" flag value` || diagnostic.Context != "" {
				t.Fatalf("equal raw identity demand: diagnostic=%#v err=%v", diagnostic, err)
			}
			diagnostic.Message = "caller-owned mutation"
			ispellAssert(t, d, "root", []string{"root"})
		}
	})
	t.Run("duplicate_numeric_delimiter", func(t *testing.T) {
		d := ispellCompile(t, "root/1\nroot/2\n", "FLAG num\nSFX 1 N 1\nSFX 1 0 s .\nSFX 2 N 1\nSFX 2 0 ed .\nSFX 12 N 1\nSFX 12 0 x .\n", "")
		ispellAssert(t, d, "roots", []string{"root"})
		ispellAssert(t, d, "rooted", []string{"root"})
		ispellAssert(t, d, "rootx", nil)
	})
	for _, tc := range []struct {
		name, dictionary, aliases, rules string
		lookupError                      bool
	}{
		{"distinct_slots", "root/1\nroot/2\n", "AF 2\nAF A\nAF B\n", "SFX AB N 1\nSFX AB 0 s .\nSFX BA N 1\nSFX BA 0 s .\n", false},
		{"same_slot", "root/1\nroot/1\n", "AF 1\nAF A\n", "SFX AA N 1\nSFX AA 0 s .\n", true},
		{"distinct_slots_equal_text", "root/1\nroot/2\n", "AF 2\nAF A\nAF A\n", "SFX AA N 1\nSFX AA 0 s .\n", false},
	} {
		t.Run("duplicate_alias_"+tc.name, func(t *testing.T) {
			d := ispellCompile(t, tc.dictionary, "FLAG long\n"+tc.aliases+tc.rules, "")
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "unknown", nil)
			if !tc.lookupError {
				ispellAssert(t, d, "roots", []string{"root"})
				return
			}
			_, err := d.Lexize(context.Background(), "roots")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
				diagnostic.Message != `invalid affix flag "A" with "long" flag value` || diagnostic.Context != "" {
				t.Fatalf("same alias identity demand: diagnostic=%#v err=%v", diagnostic, err)
			}
		})
	}
	t.Run("equal_duplicate_valid_prefix", func(t *testing.T) {
		d := ispellCompile(t, "root/AAA\nroot/AAA\n", "FLAG long\nPFX AA N 1\nPFX AA 0 pre .\nPFX BB N 1\nPFX BB 0 un .\n", "")
		ispellAssert(t, d, "preroot", []string{"root"})
		_, err := d.Lexize(context.Background(), "unroot")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
			diagnostic.Message != `invalid affix flag "A" with "long" flag value` || diagnostic.Context != "" {
			t.Fatalf("valid-prefix exhausted demand: diagnostic=%#v err=%v", diagnostic, err)
		}
		ispellAssert(t, d, "preroot", []string{"root"})
	})
	t.Run("fresh_aggregate_identity", func(t *testing.T) {
		// The compiler groups equal words stably after alias resolution.
		// In this private source-order witness, merging slots 1 and 2
		// allocates a NEW identity, so a later slot 1 must merge again.
		// A per-word seen-ID set would incorrectly leave complete AB.
		d := ispellCompile(t, "root/1\nroot/2\nroot/1\n", "FLAG long\nAF 2\nAF A\nAF B\nSFX AB N 1\nSFX AB 0 s .\nSFX BA N 1\nSFX BA 0 ed .\n", "")
		ispellAssert(t, d, "roots", []string{"root"})
		_, err := d.Lexize(context.Background(), "rooted")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
			diagnostic.Message != `invalid affix flag "A" with "long" flag value` || diagnostic.Context != "" {
			t.Fatalf("fresh aggregate identity: diagnostic=%#v err=%v", diagnostic, err)
		}
	})
	for _, tc := range []struct {
		name, unused, flag string
		accepted           bool
	}{
		{"unused_original_error", "A", "ZZ", false},
		{"unused_prefix_match", "ZZA", "ZZ", true},
		{"unused_prefix_exhausted", "ZZA", "BB", false},
	} {
		t.Run("compound_affix_table_"+tc.name, func(t *testing.T) {
			// Only alias 2 belongs to a word. Alias 1 is still an
			// earlier entry in the real affix-in-use table. Continuation
			// /2 marks the suffix as compound-relevant without demanding
			// malformed alias 1 during affix-file reading.
			b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict":  "root/2\n",
				"rules.affix": "FLAG long\nCOMPOUNDFLAG ZZ\nAF 2\nAF " + tc.unused + "\nAF ZZ\nSFX " + tc.flag + " N 1\nSFX " + tc.flag + " 0 s/2 .\n",
			}))
			if err := b.LoadDictionary(context.Background(), "words"); err != nil {
				t.Fatal(err)
			}
			if err := b.LoadAffixes(context.Background(), "rules"); err != nil {
				t.Fatal(err)
			}
			d, err := b.Finish(context.Background())
			if tc.accepted {
				if err != nil || d == nil {
					t.Fatal("prefix membership must precede malformed tail", d, err)
				}
				ispellAssert(t, d, "roots", []string{"root"})
				return
			}
			var diagnostic *sqlstate.Error
			if d != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
				diagnostic.Message != `invalid affix flag "A" with "long" flag value` || diagnostic.Context != "" {
				t.Fatalf("original table demand: dictionary=%v diagnostic=%#v err=%v", d, diagnostic, err)
			}
		})
	}
	d = ispellCompile(t, "root/A "+strings.Repeat("x", 65536)+"\ntail/A\n", "SFX A Y 1\nSFX A 0 s .\n", "")
	ispellAssert(t, d, "tails", []string{"tail"})
}
func TestIspellAffixParsing(t *testing.T) {
	for _, affix := range []string{
		"prefixes\nflag *P:\n  # ignored > 1\n. > RE # comment\nsuffixes\nflag *S:\n[^AEIOU]Y > -Y,IES # comment\n",
		"SET UTF-8\nUNKNOWN ignored\nPFX P Y 99 # ignored count\nPFX P 0 re .\nSFX S Y 1 # comment\nSFX S y ies [^aeiou]y\n",
	} {
		d := ispellCompile(t, "try/PS\n", affix, "")
		ispellAssert(t, d, "tries", []string{"try"})
		ispellAssert(t, d, "retry", []string{"try"})
		ispellAssert(t, d, "retries", []string{"try"})
	}
	for _, tc := range []struct{ data, message string }{
		{"prefixes\nPFX A Y 1\n", "affix file contains both old-style and new-style commands"},
		{"suffixes\nflag A:\n. > 1\n", "syntax error"},
		{"FLAG UTF-8\n", `Ispell dictionary supports only "default", "long", and "num" flag values`},
	} {
		b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"bad.affix": tc.data}))
		err := b.LoadAffixes(context.Background(), "bad")
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "F0000" || d.Message != tc.message || !strings.Contains(d.Context, `configuration file "bad.affix"`) {
			t.Fatalf("error=%#v (%v)", d, err)
		}
	}
	for _, line := range []string{"FLAG #\n", "FLAG#\n", "FLAG \tUTF-8\n", "FLAG LONG\n"} {
		b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
			"bad.affix": "SFX A Y 1\nSFX A 0 s .\n" + line,
		}))
		err := b.LoadAffixes(context.Background(), "bad")
		var diagnostic *sqlstate.Error
		wantContext := "line 3 of configuration file \"bad.affix\": \"" + line + "\""
		if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
			diagnostic.Message != `Ispell dictionary supports only "default", "long", and "num" flag values` ||
			diagnostic.Context != wantContext {
			t.Fatalf("%q: diagnostic=%#v err=%v want Context=%q", line, diagnostic, err, wantContext)
		}
	}
	for _, line := range []string{"AF\n", "AF \t\n", "AF # comment\n", "AF#\n", "aFextra 0\n", "AFextra\n"} {
		b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
			"bad.affix": "SFX A Y 1\nSFX A 0 s .\n" + line,
		}))
		err := b.LoadAffixes(context.Background(), "bad")
		var diagnostic *sqlstate.Error
		wantContext := "line 3 of configuration file \"bad.affix\": \"" + line + "\""
		if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
			diagnostic.Message != "invalid number of flag vector aliases" || diagnostic.Context != wantContext {
			t.Fatalf("%q: diagnostic=%#v err=%v want Context=%q", line, diagnostic, err, wantContext)
		}
	}
	for _, command := range []string{
		"COMPOUNDFLAG", "COMPOUNDBEGIN", "COMPOUNDMIDDLE", "COMPOUNDLAST",
		"COMPOUNDEND", "ONLYINCOMPOUND", "COMPOUNDPERMITFLAG", "COMPOUNDFORBIDFLAG",
	} {
		for _, ending := range []string{"\n", " \t\n", ""} {
			for _, prefix := range []string{"FLAG long\n", ""} {
				line := command + ending
				if prefix == "" && command != "COMPOUNDFLAG" {
					// These are not legacy format-switch markers.
					d := ispellCompile(t, "root\n", line, "")
					ispellAssert(t, d, "root", []string{"root"})
					continue
				}
				b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"bad.affix": prefix + line}))
				err := b.LoadAffixes(context.Background(), "bad")
				var diagnostic *sqlstate.Error
				number := 1
				if prefix != "" {
					number = 2
				}
				want := fmt.Sprintf("line %d of configuration file \"bad.affix\": \"%s\"", number, line)
				if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != "syntax error" || diagnostic.Context != want {
					t.Fatalf("%q: diagnostic=%#v err=%v want Context=%q", prefix+line, diagnostic, err, want)
				}
			}
		}
		d := ispellCompile(t, "root\n", "FLAG long\n"+command+" ZZ\n", "")
		ispellAssert(t, d, "root", []string{"root"})
	}
	b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"bad.affix": "COMPOUNDFLAG \t\nFLAG UTF-8\n"}))
	err := b.LoadAffixes(context.Background(), "bad")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != "syntax error" || diagnostic.Context != "line 1 of configuration file \"bad.affix\": \"COMPOUNDFLAG \t\n\"" {
		t.Fatal(diagnostic, err)
	}
	d := ispellCompile(t, "root\n", "FLAG long\n\n# ignored comment\nUNRECOGNIZED\n", "")
	ispellAssert(t, d, "root", []string{"root"})
	// Compound operands use the release's raw command-prefix/operand reader,
	// not the generic field parser's field-leading comment convention.
	for _, tc := range []struct{ dict, affix string }{
		{"root/Z\n", "COMPOUNDFLAGZ\n"},
		{"root/#\n", "COMPOUNDFLAG #\n"},
	} {
		d := ispellCompile(t, tc.dict, tc.affix, "")
		ispellAssert(t, d, "rootroot", []string{"root", "root"})
	}
	// NIImportAffixes forwards the complete legacy operand. Character-mode
	// dictionary flags cannot match a multi-character compound key.
	for _, tc := range []struct {
		operand string
		want    []string
	}{
		{"Z", []string{"root", "root"}},
		{"ZZ", nil},
	} {
		t.Run("legacy_compound_"+tc.operand, func(t *testing.T) {
			d := ispellCompile(t, "root/Z\n", "compoundwords controlled "+tc.operand+"\n", "")
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "rootroot", tc.want)
			ispellAssert(t, d, "unknown", nil)
		})
	}
}
func TestIspellFlags(t *testing.T) {
	for _, tc := range []struct{ dict, affix string }{
		{"root/AA", "FLAG long\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"root/1", "FLAG long\nAF 1\nAF AA\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"root/65536", "FLAG num\nSFX 65536 Y 1\nSFX 65536 0 s .\n"},
		{"root/A", "SFX A Y 1\nSFX A 0 s .\nFLAG"},
		{"root/A", "SFX A Y 1\nSFX A 0 s .\nFLAG \t\r\n"},
		{"root/A", "SFX A Y 1\nSFX A 0 s .\nFLAGdefault_extra\n"},
		{"root/AA", "SFX AA Y 1\nSFX AA 0 s .\nFLAGlong_extra\n"},
		{"root/12", "SFX 12 Y 1\nSFX 12 0 s .\nFLAGnum_extra\n"},
		{"root/AA", "FLAG long\nFLAG default\nFLAG \t\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"root/12", "FLAG num\nFLAG default\nSFX 12 Y 1\nSFX 12 0 s .\n"},
		{"root/12", "FLAG long\nFLAG num\nSFX 12 Y 1\nSFX 12 0 s .\n"},
		{"root/AA", "FLAG num\nFLAG long\nSFX AA Y 1\nSFX AA 0 s .\n"},
		{"root/1", "SFX A Y 1\nSFX A 0 s .\naFextra 1\nAfentry A\n"},
		{"root/A", "SFXextra A Y 1\nsFxrule A 0 s .\n"},
		{"root/A", "SFXextra A Y 1\nPFXrule A 0 s .\n"},
		{"root/A", "SFX A Y 1\nSFX A 0 s .\nXAF\nXSFX A 0 x .\nXPFX A 0 pre .\nPFX\nSFX A\n"},
	} {
		d := ispellCompile(t, tc.dict, tc.affix, "")
		ispellAssert(t, d, "roots", []string{"root"})
	}
	for _, affix := range []string{
		"PFXextra A Y 1\npFxrule A 0 pre .\n",
		"PFXextra A Y 1\nSFXrule A 0 pre .\n",
	} {
		d := ispellCompile(t, "root/A\n", affix, "")
		ispellAssert(t, d, "preroot", []string{"root"})
		ispellAssert(t, d, "rootpre", nil)
	}
	for _, entry := range []string{"AF\n", "AF # empty\n"} {
		d := ispellCompile(t, "root/1\n", "SFX A Y 1\nSFX A 0 s .\nAF 1\n"+entry, "")
		ispellAssert(t, d, "root", []string{"root"})
		ispellAssert(t, d, "roots", nil)
		b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
			"bad.affix": "SFX A Y 1\nSFX A 0 s .\nAF 1\nAF A\n" + entry,
		}))
		err := b.LoadAffixes(context.Background(), "bad")
		var diagnostic *sqlstate.Error
		wantContext := "line 5 of configuration file \"bad.affix\": \"" + entry + "\""
		if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" ||
			diagnostic.Message != "number of aliases exceeds specified number 1" || diagnostic.Context != wantContext {
			t.Fatalf("%q: diagnostic=%#v err=%v want Context=%q", entry, diagnostic, err, wantContext)
		}
	}
	b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"bad.affix": "FLAG num\nCOMPOUNDFLAG 65537\n"}))
	err := b.LoadAffixes(context.Background(), "bad")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Message != `affix flag "65537" is out of range` {
		t.Fatal(err)
	}
	// A dictionary alias reference rejects the table's one-past boundary;
	// the same continuation reference is deliberately treated as empty.
	for _, raw := range []string{"2", "-1", "1x"} {
		b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{"words.dict": "root/" + raw + "\n", "rules.affix": "FLAG num\nAF 1\nAF 1\nSFX 1 Y 1\nSFX 1 0 s .\n"}))
		if err := b.LoadDictionary(context.Background(), "words"); err != nil {
			t.Fatal(err)
		}
		if err := b.LoadAffixes(context.Background(), "rules"); err != nil {
			t.Fatal(err)
		}
		d, err := b.Finish(context.Background())
		var diagnostic *sqlstate.Error
		if d != nil || !errors.As(err, &diagnostic) || diagnostic.Message != fmt.Sprintf("invalid affix alias %q", raw) || diagnostic.Context != "" {
			t.Fatal(d, err)
		}
	}
	d := ispellCompile(t, "root/1\n", "FLAG num\nAF 1\nAF 1\nSFX 1 Y 1\nSFX 1 0 s/2 .\n", "")
	ispellAssert(t, d, "roots", []string{"root"})
	// With no compound directives, an invalid numeric flag is dormant until
	// a matching affix lookup demands it. Direct recognition needs no flag.
	d = ispellCompile(t, "root/65537\n", "FLAG num\nSFX 1 Y 1\nSFX 1 0 s .\n", "")
	ispellAssert(t, d, "root", []string{"root"})
	_, err = d.Lexize(context.Background(), "roots")
	if !errors.As(err, &diagnostic) || diagnostic.Message != `affix flag "65537" is out of range` || diagnostic.Context != "" {
		t.Fatal(err)
	}
	for _, raw := range []string{"A", "AAA"} {
		t.Run("incomplete_long_"+raw, func(t *testing.T) {
			b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict":  "root/" + raw + "\n",
				"rules.affix": "FLAG long\nCOMPOUNDFLAG ZZ\n",
			}))
			if err := b.LoadDictionary(context.Background(), "words"); err != nil {
				t.Fatal(err)
			}
			if err := b.LoadAffixes(context.Background(), "rules"); err != nil {
				t.Fatal(err)
			}
			d, err := b.Finish(context.Background())
			var diagnostic *sqlstate.Error
			want := `invalid affix flag "A" with "long" flag value`
			if d != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != want || diagnostic.Context != "" {
				t.Fatal(d, diagnostic, err)
			}
			d = ispellCompile(t, "root/"+raw+"\n", "FLAG long\nPFX AA N 1\nPFX AA 0 pre .\nPFX BB N 1\nPFX BB 0 un .\n", "")
			ispellAssert(t, d, "root", []string{"root"})
			ispellAssert(t, d, "unknown", nil)
			tokens := []string{"unroot"}
			if raw == "AAA" {
				// Membership found before the incomplete tail must still
				// short-circuit successfully, just like IsAffixFlagInUse.
				ispellAssert(t, d, "preroot", []string{"root"})
			} else {
				tokens = append(tokens, "preroot")
			}
			for _, token := range tokens {
				for range 2 {
					_, err = d.Lexize(context.Background(), token)
					if !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != want || diagnostic.Context != "" {
						t.Fatal(token, diagnostic, err)
					}
					diagnostic.Message = "caller-owned mutation"
					ispellAssert(t, d, "root", []string{"root"})
				}
			}
		})
	}
	// Dictionary files retain the release's printable-ASCII flag boundary.
	// AF aliases reach the real long-mode decoder with multibyte characters.
	for _, tc := range []struct{ flags, remainder string }{
		{"éé", ""}, {"Aé", ""}, {"é", "é"}, {"AAé", "é"},
	} {
		t.Run("long_alias_"+tc.flags, func(t *testing.T) {
			b := functions.NewIspellCompiler(functions.NewDictionaryFiles(map[string]string{
				"words.dict":  "root/1\n",
				"rules.affix": "FLAG long\nAF 1\nAF " + tc.flags + "\nCOMPOUNDFLAG ZZ\n",
			}))
			if err := b.LoadDictionary(context.Background(), "words"); err != nil {
				t.Fatal(err)
			}
			if err := b.LoadAffixes(context.Background(), "rules"); err != nil {
				t.Fatal(err)
			}
			d, err := b.Finish(context.Background())
			if tc.remainder == "" {
				if err != nil {
					t.Fatal(err)
				}
				ispellAssert(t, d, "root", []string{"root"})
			} else {
				var diagnostic *sqlstate.Error
				want := fmt.Sprintf("invalid affix flag \"%s\" with \"long\" flag value", tc.remainder)
				if d != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "F0000" || diagnostic.Message != want || diagnostic.Context != "" {
					t.Fatal(d, diagnostic, err)
				}
			}
		})
	}
}
func TestIspellNormalization(t *testing.T) {
	d := ispellCompile(t, "root/PS\nrooting/SB\n", "PFX P Y 1\nPFX P 0 re .\nPFX B Y 1\nPFX B 0 un .\nSFX S Y 1\nSFX S 0 ing .\n", "root\n")
	ispellAssert(t, d, "root", []string{})
	ispellAssert(t, d, "rooting", []string{"rooting"})
	ispellAssert(t, d, "rerooting", []string{})
	ispellAssert(t, d, "unknown", nil)
	if !d.Configured("words", "rules", "removed") || d.Filtering() {
		t.Fatal("immutable configuration")
	}
	atLimit := strings.Repeat("a", 256)
	overLimit := atLimit + "a"
	d = ispellCompile(t, atLimit+"\n"+overLimit+"\n", "", "")
	ispellAssert(t, d, atLimit, []string{atLimit})
	ispellAssert(t, d, overLimit, nil)
}
func TestIspellCompounds(t *testing.T) {
	d := ispellCompile(t, "sun/B\nmid/M\nset/E\nlink/ZE\nonly/LO\nplain/Z\njoined/O\njoined/B\n", "COMPOUNDBEGIN B\nCOMPOUNDMIDDLE M\nCOMPOUNDEND E\nCOMPOUNDFLAG Z\nONLYINCOMPOUND O\nCOMPOUNDPERMITFLAG P\nCOMPOUNDFORBIDFLAG F\nSFX L Y 2\nSFX L 0 x/P .\nSFX L 0 q/F .\n", "sun\n")
	ispellAssert(t, d, "sunset", []string{"set"})
	ispellAssert(t, d, "sunmidset", []string{"mid", "set"})
	ispellAssert(t, d, "setsun", nil)
	ispellAssert(t, d, "sunsetmid", nil)
	ispellAssert(t, d, "only", nil)
	ispellAssert(t, d, "onlyxlink", []string{"only", "link"})
	ispellAssert(t, d, "onlyqlink", nil)
	// Merging an ordinary BEGIN record with ONLYINCOMPOUND removes ONLY,
	// but must not preserve its otherwise-implied MIDDLE/END positions.
	ispellAssert(t, d, "joined", []string{"joined"})
	ispellAssert(t, d, "joinedset", []string{"joined", "set"})
	ispellAssert(t, d, "sunjoinedset", nil)
	ispellAssert(t, d, "setjoined", nil)
	for _, dictionary := range []string{"root/O\nroot/O\n", "root/O\nroot/Z\n", "root/Z\nroot/O\n"} {
		t.Run("duplicate_compound_flags_"+dictionary, func(t *testing.T) {
			d := ispellCompile(t, dictionary, "ONLYINCOMPOUND O\nCOMPOUNDFLAG Z\n", "")
			if dictionary == "root/O\nroot/O\n" {
				ispellAssert(t, d, "root", nil)
			} else {
				ispellAssert(t, d, "root", []string{"root"})
			}
			ispellAssert(t, d, "rootroot", []string{"root", "root"})
		})
	}
}
func TestIspellLexicalVariants(t *testing.T) {
	d := ispellCompile(t, "sun/B\nset/E\nsunset/B\n", "FLAG default\nCOMPOUNDBEGIN B\nCOMPOUNDEND E\n", "")
	got, err := d.Lexize(context.Background(), "sunset")
	want := []fts.Lexeme{{Text: "sunset", Variant: 1}, {Text: "sun", Variant: 2}, {Text: "set", Variant: 2}}
	if err != nil || !got.Recognized || len(got.Lexemes) != len(want) {
		t.Fatal(got, err)
	}
	for i, lexeme := range want {
		if got.Lexemes[i] != lexeme {
			t.Fatal(got.Lexemes, want)
		}
	}
}
func TestIspellConditions(t *testing.T) {
	d := ispellCompile(t, "try/A\nbook/B\nsky/C\n", "SFX A Y 1\nSFX A y ies [^aeiou]y{1}\nSFX B Y 1\nSFX B 0 ed k{1}\nSFX C Y 1\nSFX C y 0 y*\n", "")
	for _, tc := range []struct{ token, root string }{{"tries", "try"}, {"booked", "book"}, {"sk", "sky"}} {
		ispellAssert(t, d, tc.token, []string{tc.root})
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			for range 20 {
				v, err := d.Lexize(context.Background(), "tries")
				if err != nil || !v.Recognized || len(v.Lexemes) != 1 {
					failures <- fmt.Errorf("concurrent condition: %v %v", v, err)
					return
				}
				if v.Lexemes[0].Text != "try" {
					failures <- fmt.Errorf("concurrent lexeme: %v", v.Lexemes)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

type ispellCancelContext struct {
	context.Context
	cancel       context.CancelFunc
	after, calls int
}

func (c *ispellCancelContext) Err() error {
	c.calls++
	if c.calls == c.after {
		c.cancel()
	}
	return c.Context.Err()
}
func ispellCancellation(after int) *ispellCancelContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &ispellCancelContext{Context: ctx, cancel: cancel, after: after}
}
func TestIspellCancellation(t *testing.T) {
	files := functions.NewDictionaryFiles(map[string]string{"words.dict": strings.Repeat("root/A\n", 1000), "rules.affix": "SFX A Y 1\nSFX A 0 s .\n"})
	for _, after := range []int{1, 30, 1000} {
		ctx := ispellCancellation(after)
		b := functions.NewIspellCompiler(files)
		err := b.LoadDictionary(ctx, "words")
		ctx.cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reader cancellation", after, err)
		}
	}
	b := functions.NewIspellCompiler(files)
	if err := b.LoadDictionary(context.Background(), "words"); err != nil {
		t.Fatal(err)
	}
	if err := b.LoadAffixes(context.Background(), "rules"); err != nil {
		t.Fatal(err)
	}
	ctx := ispellCancellation(4)
	compiled, err := b.Finish(ctx)
	ctx.cancel()
	if compiled != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("index construction cancellation", compiled, err)
	}
	d := ispellCompile(t, "root/A\n", "SFX A Y 1\nSFX A 0 s .\n", "")
	ctx = ispellCancellation(3)
	value, err := d.Lexize(ctx, "roots")
	ctx.cancel()
	if value.Recognized || len(value.Lexemes) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("normalization cancellation", value, err)
	}
}
