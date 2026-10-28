package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func ftsConvertSetting(t *testing.T, p *fixture.Pair, c *sql.Conn) func() {
	t.Helper()
	restore := ftsConvertRestore(t, p, c)
	if _, err := c.ExecContext(p.Ctx, "SET default_text_search_config='english'"); err != nil {
		t.Fatal(err)
	}
	return restore
}

func ftsConvertInput(name string) string {
	if name == "to_tsquery" {
		return "The & Rats"
	}
	return "The Rats"
}
func ftsConvertExpected(name string, english bool) string {
	if english {
		if name == "to_tsvector" {
			return "'rat':2"
		}
		return "'rat'"
	}
	switch name {
	case "to_tsvector":
		return "'rats':2 'the':1"
	case "phraseto_tsquery":
		return "'the' <-> 'rats'"
	default:
		return "'the' & 'rats'"
	}
}
func ftsConvertRestore(t *testing.T, p *fixture.Pair, c *sql.Conn) func() {
	t.Helper()
	var prior string
	if err := c.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&prior); err != nil {
		t.Fatal(err)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "SET default_text_search_config='"+strings.ReplaceAll(prior, "'", "''")+"'"); err != nil {
			t.Error(err)
		}
	}
}
func TestReferenceFTSConvertDefaults(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertRestore(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, english := range []bool{false, true, false} {
			setting := "simple"
			if english {
				setting = "english"
			}
			run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
			for _, name := range ftsConvertNames {
				s := ftsConvertValue("SELECT "+name+"($1::text) AS normalized", name, ftsConvertExpected(name, english), ftsConvertInput(name))
				run(s)
				run(fixture.Prepared(s))
				s = ftsConvertValue("SELECT "+name+"('english'::regconfig,$1::text) AS normalized", name, ftsConvertExpected(name, true), ftsConvertInput(name))
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(fixture.Exec("SET default_text_search_config='english'", 0))
		invalid := fixture.ErrorDiagnostic("SET default_text_search_config='convert_missing'", fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "default_text_search_config": "convert_missing"`))
		run(invalid)
		run(fixture.Prepared(invalid))
		run(fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{"pg_catalog.english"}}))
		for _, name := range ftsConvertNames {
			run(ftsConvertValue("SELECT "+name+"($1::text) AS normalized", name, ftsConvertExpected(name, true), ftsConvertInput(name)))
		}
		run(fixture.Exec("SET default_text_search_config='simple'", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
		for _, name := range ftsConvertNames {
			run(ftsConvertValue("SELECT "+name+"($1::text) AS normalized", name, ftsConvertExpected(name, true), ftsConvertInput(name)))
		}
		run(fixture.Exec("ROLLBACK", 0))
		for _, name := range ftsConvertNames {
			run(ftsConvertValue("SELECT "+name+"($1::text) AS normalized", name, ftsConvertExpected(name, false), ftsConvertInput(name)))
		}
	})
}
func TestReferenceFTSConvertPrepared(t *testing.T) {
	for _, name := range ftsConvertNames {
		t.Run(name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertRestore(t, p, c)()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("SET default_text_search_config='simple'", 0))
				for _, parameter := range []bool{false, true} {
					input := "'" + ftsConvertInput(name) + "'::text"
					var args []any
					if parameter {
						input = "$1::text"
						args = []any{ftsConvertInput(name)}
					}
					query := "SELECT " + name + "(" + input + ") AS normalized"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					for _, english := range []bool{false, true, false} {
						setting := "simple"
						if english {
							setting = "english"
						}
						run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConvertValue(query, name, ftsConvertExpected(name, english), args...))
					}
					stmt.Close()
				}
				dynamic := "SELECT " + name + "($1::regconfig,$2::text) AS normalized"
				dynamicStatement, err := c.PrepareContext(p.Ctx, dynamic)
				if err != nil {
					t.Fatal(err)
				}
				dynamicHeld := fixture.HeldStatement{Executor: c, Statement: dynamicStatement, SQL: dynamic}
				for _, tc := range []struct {
					config any
					want   any
				}{
					{"simple", ftsConvertExpected(name, false)},
					{"english", ftsConvertExpected(name, true)},
					{[]byte("simple"), ftsConvertExpected(name, false)},
					{nil, nil},
				} {
					step := ftsConvertValue(dynamic, name, tc.want, tc.config, ftsConvertInput(name))
					run(step)
					fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, step)
				}
				diagnostic := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "convert_missing" does not exist`)
				diagnostic.Context = "unnamed portal parameter $1 = '...'"
				badConfig := fixture.ErrorDiagnostic(dynamic, diagnostic, "convert_missing", ftsConvertInput(name))
				badConfig.SelectRows, badConfig.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, badConfig)
				dynamicStatement.Close()
				run(fixture.Exec("PREPARE convert_sql(text) AS SELECT "+name+"($1) AS normalized", 0))
				run(ftsConvertValue("EXECUTE convert_sql('"+ftsConvertInput(name)+"')", name, ftsConvertExpected(name, false)))
				run(fixture.Exec("SET default_text_search_config='english'", 0))
				run(ftsConvertValue("EXECUTE convert_sql('"+ftsConvertInput(name)+"')", name, ftsConvertExpected(name, true)))
				run(fixture.Exec("DEALLOCATE convert_sql", 0))
			})
		})
	}
}
func TestReferenceFTSConvertConfiguration(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.convert_config (COPY=pg_catalog.simple)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.convert_config DROP MAPPING FOR asciiword", 0))
		held := make(map[string]fixture.HeldStatement)
		for _, name := range ftsConvertNames {
			query := "SELECT " + name + "('public.convert_config'::regconfig,$1::text) AS normalized"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[name] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		}
		for _, name := range ftsConvertNames {
			input, want := "The 42", "'42'"
			if name == "to_tsquery" {
				input = "The & 42"
			}
			if name == "to_tsvector" {
				want = "'42':1"
			}
			s := ftsConvertValue("SELECT "+name+"('public.convert_config'::regconfig,$1::text) AS normalized", name, want, input)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held[name], b.Recorder, s)
		}
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.convert_stops (TEMPLATE=simple,STOPWORDS=english,ACCEPT=false)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.convert_config ADD MAPPING FOR asciiword WITH public.convert_stops,pg_catalog.simple", 0))
		for _, name := range ftsConvertNames {
			input := ftsConvertInput(name)
			want := "'rats'"
			if name == "to_tsvector" {
				want = "'rats':2"
			}
			s := ftsConvertValue("SELECT "+name+"('public.convert_config'::regconfig,$1::text) AS normalized", name, want, input)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held[name], b.Recorder, s)
		}
		constants := make(map[string]fixture.HeldStatement)
		for _, name := range ftsConvertNames {
			query := "SELECT " + name + "('public.convert_config'::regconfig,'The'::text) AS normalized"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			constants[name] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			fixture.RunStep(t, p.Ctx, constants[name], b.Recorder, ftsConvertValue(query, name, ""))
		}
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.convert_stops (STOPWORDS)", 0))
		for _, name := range ftsConvertNames {
			s := ftsConvertValue("SELECT "+name+"('public.convert_config'::regconfig,$1::text) AS normalized", name, ftsConvertExpected(name, false), ftsConvertInput(name))
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held[name], b.Recorder, s)
			fixture.RunStep(t, p.Ctx, constants[name], b.Recorder, ftsConvertValue(constants[name].SQL, name, ""))
			fresh := "'the'"
			if name == "to_tsvector" {
				fresh = "'the':1"
			}
			run(ftsConvertValue(constants[name].SQL, name, fresh))
		}
		run(fixture.Exec("CREATE EXTENSION unaccent", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.convert_config ALTER MAPPING FOR word WITH public.unaccent,pg_catalog.simple", 0))
		for _, name := range ftsConvertNames {
			want := "'hotel'"
			if name == "to_tsvector" {
				want = "'hotel':1"
			}
			s := ftsConvertValue("SELECT "+name+"('public.convert_config'::regconfig,$1::text) AS normalized", name, want, "Hôtel")
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held[name], b.Recorder, s)
		}
	})
}
