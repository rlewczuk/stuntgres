package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceIspellSQLDiagnostics(t *testing.T) {
	ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE TEXT SEARCH DICTIONARY ispell_missing_template (DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", "42P17", "text search template is required"},
			{"CREATE TEXT SEARCH DICTIONARY ispell_bad_template (TEMPLATE=ispell_absent)", "42704", `text search template "ispell_absent" does not exist`},
			{"ALTER TEXT SEARCH DICTIONARY ispell_absent (dummy)", "42704", `text search dictionary "ispell_absent" does not exist`},
			{"DROP TEXT SEARCH DICTIONARY ispell_absent", "42704", `text search dictionary "ispell_absent" does not exist`},
			{"CREATE TEXT SEARCH DICTIONARY ispell_bare_dict (TEMPLATE=ispell,DictFile,AffFile=tr_ispell_legacy)", "42601", "dictfile requires a parameter"},
			{"CREATE TEXT SEARCH DICTIONARY ispell_bare_affix (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile)", "42601", "afffile requires a parameter"},
			{"CREATE TEXT SEARCH DICTIONARY ispell_bare_stop (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy,StopWords)", "42601", "stopwords requires a parameter"},
		} {
			dictionaryError(t, p, b, c, tc.q, tc.code, tc.message)
		}
		ispellCreate(t, p, b, c, "ispell_collision", "legacy", "")
		ns := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
		d := dictionaryDiagnostic("23505", `duplicate key value violates unique constraint "pg_ts_dict_dictname_index"`)
		d.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(ispell_collision, %d) already exists.", ns)
		d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_ts_dict", "pg_ts_dict_dictname_index"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY ispell_collision (TEMPLATE=ispell,DictFile=tr_ispell_legacy,AffFile=tr_ispell_legacy)", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		ispellCreate(t, p, b, c, "ispell_other", "legacy", "")
		dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY ispell_other RENAME TO ispell_collision", "42710", `text search dictionary "ispell_collision" already exists in schema "public"`)
		for _, q := range []string{
			"/*é*/ SELECT ts_lexize('ispell_collision','book') unexpected extra",
			"/*é*/ SELECT ts_lexize('ispell_collision','book') +",
		} {
			d := dictionaryDiagnostic("42601", "syntax error at end of input")
			d.Position = fixture.Position(utf8.RuneCountInString(q) + 1)
			if i := strings.Index(q, "extra"); i >= 0 {
				d.Message = `syntax error at or near "extra"`
				d.Position = fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		ispellToken(t, p, b, c, "ispell_collision", "rebookings", "{booking,book}")
	})
}

func TestReferenceIspellAlterDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, affix, code, message, text string
		line                             int
	}{
		{"legacy_syntax", "tr_ispell_syntax", "F0000", "syntax error", ". > 1\n", 3},
		{"unsupported_flags", "tr_ispell_utf8", "F0000", `Ispell dictionary supports only "default", "long", and "num" flag values`, "FLAG UTF-8\n", 1},
		{"mixed", "tr_ispell_mixed", "F0000", "affix file contains both old-style and new-style commands", "PFX A Y 1\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ispellScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ispellCreate(t, p, b, c, "ispell_alter_error", "legacy", "")
				var oid, owner, template int64
				var options string
				snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='ispell_alter_error'"
				if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&oid, &owner, &template, &options); err != nil {
					t.Fatal(err)
				}
				for _, prepared := range []bool{false, true} {
					d := dictionaryDiagnostic(tc.code, tc.message)
					d.Context = ispellReadContext(b, tc.affix+".affix", tc.line, tc.text)
					s := fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY ispell_alter_error (StopWords=tr_ispell_book,AffFile="+tc.affix+")", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					var afterID, afterOwner, afterTemplate int64
					var after string
					if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&afterID, &afterOwner, &afterTemplate, &after); err != nil || afterID != oid || afterOwner != owner || afterTemplate != template || after != options {
						t.Fatal("rejected initialization published", afterID, afterOwner, afterTemplate, after, err)
					}
					ispellToken(t, p, b, c, "ispell_alter_error", "unbook", "{book}")
				}
			})
		})
	}
}
