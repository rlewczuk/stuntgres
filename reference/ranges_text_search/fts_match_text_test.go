package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSMatchText(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		for _, tc := range []struct {
			expression string
			want       bool
		}{
			{"'fat cats ate rats'::text @@ 'cat & rat'::tsquery", true},
			{"'cat'::text @@ 'cat | rat'::text", false},
			{"'cat'::text @@ 'cat | rat'::tsquery", true},
			{"'cat rat'::text @@ 'cat | rat'::text", true},
			{"'cat'::text @@ 'cat &'::text", true},
			{"'fat cats ate rats'::tsvector @@ to_tsquery('english','fat & rat')", false},
			{"to_tsvector('english','fat cats ate rats') @@ to_tsquery('english','fat & rat')", true},
			{"'Rats'::tsvector @@ 'rat'::tsquery", false},
			{"to_tsvector('english','Rats') @@ 'rat'::tsquery", true},
			{"'Rats'::text @@ 'rat'::tsquery", true},
			{"''::text @@ 'cat'::text", false},
			{"'cat'::text @@ ''::text", false},
			{"'cat'::text @@ 'the'::text", false},
		} {
			s := ftsMatchStep("SELECT "+tc.expression, "?column?", [][]any{{tc.want}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
	// Notice-enabled public pool independently verifies phase, count and every
	// optional field, including no Prepare notice and no cached replay.
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer ftsConvertSetting(t, p, c)()
		for _, input := range []string{"", "the"} {
			for _, parameter := range []bool{false, true} {
				rhs := ftsTypesQuote(input) + "::text"
				var args []any
				if parameter {
					rhs, args = "$1::text", []any{input}
				}
				query := "SELECT 'cat'::text @@ " + rhs
				s := ftsMatchStep(query, "?column?", [][]any{{false}}, args...)
				fixture.RunStep(t, p.Ctx, c, recorder, s)
				ftsConvertNotice(t, &notices, input, 1)
				fixture.RunStep(t, p.Ctx, c, recorder, fixture.Prepared(s))
				ftsConvertNotice(t, &notices, input, 1)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				ftsConvertNotice(t, &notices, input, 0)
				func() {
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					for range 2 {
						fixture.RunStep(t, p.Ctx, held, recorder, s)
						ftsConvertNotice(t, &notices, input, 1)
					}
				}()
			}
			for _, query := range []string{"SELECT NULL::text @@ $1::text", "SELECT $1::text @@ NULL::text"} {
				s := ftsMatchStep(query, "?column?", [][]any{{nil}}, input)
				fixture.RunStep(t, p.Ctx, c, recorder, s)
				ftsConvertNotice(t, &notices, input, 0)
				fixture.RunStep(t, p.Ctx, c, recorder, fixture.Prepared(s))
				ftsConvertNotice(t, &notices, input, 0)
			}
		}
	})
}

// Prepare under english and execute after switching on the same physical owner.
// Stable text conversion must not retain a default or an immutable BOOL fold.
func ftsMatchDefaults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("SET default_text_search_config='english'", 0))
	for _, signature := range ftsMatchSignatures[2:] {
		for _, parameter := range []bool{false, true} {
			left := "'Rats'::text"
			var args []any
			if parameter {
				left, args = "$1::text", []any{"Rats"}
			}
			query := "SELECT " + left + " @@ 'rat'::" + signature.right
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, setting := range []string{"english", "simple", "english"} {
					run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
					s := ftsMatchStep(query, "?column?", [][]any{{setting == "english"}}, args...)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					for _, expression := range []string{
						"to_tsvector('english','Rats') @@ to_tsquery('english','rat')",
						"to_tsquery('english','rat') @@ to_tsvector('english','Rats')",
					} {
						run(ftsMatchStep("SELECT "+expression, "?column?", [][]any{{true}}))
					}
				}
				run(fixture.Exec("BEGIN", 0))
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Error(err)
					}
				}()
				run(fixture.Exec("SET LOCAL default_text_search_config='simple'", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsMatchStep(query, "?column?", [][]any{{false}}, args...))
				run(fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsMatchStep(query, "?column?", [][]any{{true}}, args...))
			}()
		}
	}
}

func TestReferenceFTSMatchDefaultsPrepared(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		ftsMatchDefaults(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.match_config (COPY=pg_catalog.simple)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.match_stops (TEMPLATE=simple,STOPWORDS=english,ACCEPT=false)", 0))
		run(fixture.Exec("SET default_text_search_config='public.match_config'", 0))
		for _, signature := range ftsMatchSignatures[2:] {
			query := "SELECT $1::text @@ $2::" + signature.right
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, change := range []struct {
					ddl  string
					want bool
				}{
					{"ALTER TEXT SEARCH CONFIGURATION public.match_config ALTER MAPPING FOR asciiword WITH pg_catalog.simple", true},
					{"ALTER TEXT SEARCH CONFIGURATION public.match_config DROP MAPPING FOR asciiword", false},
					{"ALTER TEXT SEARCH CONFIGURATION public.match_config ADD MAPPING FOR asciiword WITH public.match_stops,pg_catalog.simple", false},
					{"ALTER TEXT SEARCH DICTIONARY public.match_stops (STOPWORDS)", true},
				} {
					run(fixture.Exec(change.ddl, 0))
					s := ftsMatchStep(query, "?column?", [][]any{{change.want}}, "the", "the")
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}()
			// Restore dictionary stopwords so the next signature repeats the same transition.
			run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.match_stops (STOPWORDS=english)", 0))
		}
	})
}
