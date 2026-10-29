package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSJSONPrepared(t *testing.T) {
	for _, form := range ftsJSONForms() {
		t.Run(form.name+"_"+form.typ+"_"+map[bool]string{true: "explicit", false: "default"}[form.explicit], func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertRestore(t, p, c)()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, literal := range []bool{false, true} {
					query, args := form.query(literal, "english", `"The Rats"`, `"string"`)
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					for _, setting := range []string{"simple", "english", "simple"} {
						run(fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
						want := "'rats':2 'the':1"
						if form.explicit || setting == "english" {
							want = "'rat':2"
						}
						s := ftsJSONValue(query, want, args...)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'rat':2", args...))
					run(fixture.Exec("ROLLBACK", 0))
					want := "'rats':2 'the':1"
					if form.explicit {
						want = "'rat':2"
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, want, args...))
					stmt.Close()
				}
				if !form.filtered {
					return
				}
				query, _ := form.query(false, "english", `"cat"`, `"string"`)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, tc := range []ftsJSONCase{
					{"simple", `"The Rats"`, `"string"`, "'rats':2 'the':1"},
					{"english", `123`, `"numeric"`, "'123':1"},
					{"english", `"dog"`, `[]`, ""},
					{"english", `"cat"`, `"string"`, "'cat':1"},
				} {
					_, args := form.query(false, tc.config, tc.document, tc.filter)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, tc.want, args...))
				}
				_, args := form.query(false, "english", `"cat"`, `{}`)
				ftsJSONErrorRoutes(t, p, b, c, held, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("22023", "wrong flag type, only arrays and scalars are allowed"), args...))
				_, args = form.query(false, "english", `"dog"`, `"string"`)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsJSONValue(query, "'dog':1", args...))
			})
		})
	}
	t.Run("immutable_configuration", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.json_config (COPY=pg_catalog.simple)", 0))
			run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.json_stops (TEMPLATE=simple,STOPWORDS=english,ACCEPT=false)", 0))
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.json_config ALTER MAPPING FOR asciiword WITH public.json_stops,pg_catalog.simple", 0))
			var held []fixture.HeldStatement
			for _, form := range ftsJSONForms() {
				if !form.explicit {
					continue
				}
				query, _ := form.query(true, "public.json_config", `"The"`, `"string"`)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				held = append(held, h)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, ftsJSONValue(query, ""))
			}
			run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.json_stops (STOPWORDS)", 0))
			for _, h := range held {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, ftsJSONValue(h.SQL, ""))
				run(ftsJSONValue(h.SQL, "'the':1"))
				run(fixture.Prepared(ftsJSONValue(h.SQL, "'the':1")))
			}
			for _, form := range ftsJSONForms() {
				if !form.explicit {
					continue
				}
				query, args := form.query(false, "public.json_config", `"The"`, `"string"`)
				run(ftsJSONValue(query, "'the':1", args...))
				run(fixture.Prepared(ftsJSONValue(query, "'the':1", args...)))
			}
		})
	})
}
