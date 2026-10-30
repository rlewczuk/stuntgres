package reference_test

import (
	"database/sql"
	"fmt"
	"path"
	"testing"

	"stuntgres/reference/fixture"
)

func ispellResourceName(b fixture.Backend, name string) string {
	if b.PG {
		return path.Join(fixture.TextSearchDataDirectory, name)
	}
	return name
}

// Every field is asserted by the shared fixture, including empty optional
// fields. These are independently source-grounded initial expectations, not
// observations. Root oracle discovery resolves any exposed Context or phase
// differences before native implementation; no recorder learns expectations.
func ispellReadContext(b fixture.Backend, name string, line int, text string) string {
	context := fmt.Sprintf("line %d of configuration file %q", line, ispellResourceName(b, name))
	if text != "" {
		context += ": \"" + text + "\""
	}
	return context
}

func TestReferenceIspellDiagnostics(t *testing.T) {
	type diagnosticCase struct {
		name, options, code, message string
		file                         string
		line                         int
		text                         string
	}
	valid := "DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy"
	cases := []diagnosticCase{
		{name: "missing_both", code: "22023", message: "missing AffFile parameter"},
		{name: "missing_affix", options: "DictFile=tr_ispell_legacy", code: "22023", message: "missing AffFile parameter"},
		{name: "missing_dictionary", options: "AffFile=tr_ispell_legacy", code: "22023", message: "missing DictFile parameter"},
		{name: "duplicate_dict", options: valid + ",DictFile=tr_ispell_legacy", code: "22023", message: "multiple DictFile parameters"},
		{name: "duplicate_affix", options: valid + ",AffFile=tr_ispell_legacy", code: "22023", message: "multiple AffFile parameters"},
		{name: "duplicate_stop", options: valid + ",StopWords=tr_ispell_book,StopWords=tr_ispell_book", code: "22023", message: "multiple StopWords parameters"},
		{name: "unknown", options: valid + ",unknown=1", code: "22023", message: `unrecognized Ispell parameter: "unknown"`},
		{name: "quoted_name", options: valid + `,"DictFile"=tr_ispell_legacy`, code: "22023", message: `unrecognized Ispell parameter: "DictFile"`},
		{name: "bad_basename", options: "DictFile='../bad',AffFile=tr_ispell_legacy", code: "22023", message: `invalid text search configuration file name "../bad"`},
		{name: "uppercase_basename", options: "DictFile='BAD',AffFile=tr_ispell_legacy", code: "22023", message: `invalid text search configuration file name "BAD"`},
		{name: "missing_dict_file", options: "DictFile=tr_ispell_absent,AffFile=tr_ispell_legacy", code: "F0000", message: "dictionary", file: "tr_ispell_absent.dict"},
		{name: "missing_affix_file", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_absent", code: "F0000", message: "affix", file: "tr_ispell_absent.affix"},
		{name: "missing_stop_file", options: valid + ",StopWords=tr_ispell_absent", code: "F0000", message: "stop-word", file: "tr_ispell_absent.stop"},
		{name: "empty_dict_basename", options: "DictFile='',AffFile=tr_ispell_legacy", code: "F0000", message: "dictionary", file: ".dict"},
		{name: "empty_affix_basename", options: "DictFile=tr_ispell_legacy,AffFile=''", code: "F0000", message: "affix", file: ".affix"},
		{name: "missing_before_duplicate", options: "DictFile=tr_ispell_absent,DictFile=tr_ispell_legacy", code: "F0000", message: "dictionary", file: "tr_ispell_absent.dict"},
		{name: "unknown_before_required", options: "bad=1", code: "22023", message: `unrecognized Ispell parameter: "bad"`},
		{name: "mixed", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_mixed", code: "F0000", message: "affix file contains both old-style and new-style commands", file: "tr_ispell_mixed.affix", line: 2, text: "PFX A Y 1\n"},
		{name: "unsupported_utf8_flags", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_utf8", code: "F0000", message: `Ispell dictionary supports only "default", "long", and "num" flag values`, file: "tr_ispell_utf8.affix", line: 1, text: "FLAG UTF-8\n"},
		{name: "legacy_syntax", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_syntax", code: "F0000", message: "syntax error", file: "tr_ispell_syntax.affix", line: 3, text: ". > 1\n"},
		{name: "regex", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_regex", code: "2201B", message: "invalid regular expression: parentheses () not balanced", file: "tr_ispell_regex.affix", line: 2, text: "SFX A 0 s (\n"},
		{name: "alias_pair", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_long", code: "F0000", message: `invalid affix alias "GJUS"`},
		{name: "alias_zero", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_alias_zero", code: "F0000", message: "invalid number of flag vector aliases", file: "tr_ispell_alias_zero.affix", line: 2, text: "AF 0\n"},
		{name: "alias_extra", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_alias_extra", code: "F0000", message: "number of aliases exceeds specified number 1", file: "tr_ispell_alias_extra.affix", line: 4, text: "AF BB\n"},
		{name: "alias_short", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_alias_short", code: "F0000", message: "number of aliases is less than specified number 2"},
		{name: "flag_range", options: "DictFile=tr_ispell_num,AffFile=tr_ispell_range", code: "F0000", message: `affix flag "65537" is out of range`, file: "tr_ispell_range.affix", line: 2, text: "COMPOUNDFLAG 65537\n"},
		{name: "dictionary_encoding", options: "DictFile=tr_ispell_badbytes,AffFile=tr_ispell_legacy", code: "22021", message: `invalid byte sequence for encoding "UTF8": 0xff`, file: "tr_ispell_badbytes.dict", line: 2},
		{name: "affix_encoding", options: "DictFile=tr_ispell_legacy,AffFile=tr_ispell_badbytes", code: "22021", message: `invalid byte sequence for encoding "UTF8": 0xff`, file: "tr_ispell_badbytes.affix", line: 2},
		{name: "stop_encoding", options: valid + ",StopWords=tr_ispell_badbytes", code: "22021", message: `invalid byte sequence for encoding "UTF8": 0xff`, file: "tr_ispell_badbytes.stop", line: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
					ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						options := "TEMPLATE=pg_catalog.ispell"
						if tc.options != "" {
							options += "," + tc.options
						}
						d := dictionaryDiagnostic(tc.code, tc.message)
						if tc.file != "" && tc.line == 0 {
							d.Message = fmt.Sprintf("could not open %s file %q: No such file or directory", tc.message, ispellResourceName(b, tc.file))
						}
						if tc.line != 0 {
							d.Context = ispellReadContext(b, tc.file, tc.line, tc.text)
						}
						s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY ispell_rejected ("+options+")", d)
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='ispell_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
					})
				})
			}
		})
	}
}

func TestReferenceIspellOptions(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ispellCreate(t, p, b, c, "ispell_options", "legacy", "")
		q := "SELECT ts_lexize('ispell_options'::regdictionary,$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			options string
			want    any
		}{
			{"StopWords=tr_ispell_book", "{}"}, {"StopWords", "{book}"},
			{"StopWords=tr_ispell_book,StopWords=''", "{book}"}, {"dummy", "{book}"},
			{"AffFile=tr_ispell_default,DictFile=tr_ispell_default", "{book}"},
			{"StopWords=tr_ispell_book", "{}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_options ("+tc.options+")", 0)
			dictionaryResult(t, p, b, held, q, tc.want, "unbook")
		}
		var id, owner, template int64
		var options string
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='ispell_options'"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &options); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || owner <= 0 || template != 3733 || options != "afffile = 'tr_ispell_default', dictfile = 'tr_ispell_default', stopwords = 'tr_ispell_book'" {
			t.Fatal(id, owner, template, options)
		}
		for _, tc := range []struct{ options, message string }{
			{"AffFile", "missing AffFile parameter"}, {"DictFile", "missing DictFile parameter"},
			{"bad=1", `unrecognized Ispell parameter: "bad"`},
			{`"AffFile"=tr_ispell_legacy`, `unrecognized Ispell parameter: "AffFile"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_options ("+tc.options+")", "22023", tc.message)
			var afterID, afterOwner, afterTemplate int64
			var after string
			if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&afterID, &afterOwner, &afterTemplate, &after); err != nil || afterID != id || afterOwner != owner || afterTemplate != template || after != options {
				t.Fatal("failed ALTER published", afterID, afterOwner, afterTemplate, after, err)
			}
			dictionaryResult(t, p, b, held, q, "{}", "unbook")
		}
	})
}
