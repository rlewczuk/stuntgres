package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCompositeExprNamedTargetDiagnostics(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "composite_diag_a")
	fixture.SchemaReferenceCleanup(t, "composite_diag_b")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA composite_diag_a",
				"CREATE SCHEMA composite_diag_b",
				`CREATE TYPE composite_diag_a."Pair One" AS (i integer, t text)`,
				"CREATE TYPE composite_diag_a.other_pair AS (i integer, t text)",
				`CREATE TYPE composite_diag_b."Pair One" AS (i integer, t text)`,
				"CREATE TYPE composite_diag_b.other_pair AS (i integer, t text)",
				`CREATE DOMAIN composite_diag_a.pair_domain AS composite_diag_a."Pair One"`,
				`CREATE TYPE composite_diag_a.outer_pair AS (p composite_diag_a."Pair One", d composite_diag_a.pair_domain)`,
				"CREATE TABLE composite_diag_a.holder (k integer, v composite_diag_a.outer_pair)",
				"SET search_path = composite_diag_a, public",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO composite_diag_a.holder VALUES (1, ROW(ROW(1,'one'),ROW(2,'two'))::composite_diag_a.outer_pair), (2, ROW(ROW(3,'three'),ROW(4,'four'))::composite_diag_a.outer_pair)", 2))
			stmt, err := conn.PrepareContext(p.Ctx, "UPDATE composite_diag_a.holder SET v.p.i=$1 WHERE k=1")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stmt.Close() })
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: "UPDATE composite_diag_a.holder SET v.p.i=$1 WHERE k=1"}
			unchanged := func() {
				run(fixture.Select("SELECT k, ((v).p).i AS p_i, ((v).p).t AS p_t, ((v).d).i AS d_i, ((v).d).t AS d_t FROM composite_diag_a.holder ORDER BY k", []string{"k", "p_i", "p_t", "d_i", "d_t"}, []string{"INT4", "INT4", "TEXT", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "one", int64(2), "two"}, {int64(2), int64(3), "three", int64(4), "four"}}))
			}
			type failure struct {
				query, field, destination, source, position string
				nonarray                          bool
			}
			check := func(c failure) {
				message := "subfield " + `"` + c.field + `"` + " is of type " + c.destination + " but expression is of type " + c.source
				hint := "You will need to rewrite or cast the expression."
				if c.nonarray {
					message = "cannot subscript type " + c.destination + " because it does not support subscripting"
					hint = ""
				}
				want := fixture.ControlsReferenceDiagnostic("42804", message)
				want.Hint = hint
				want.Position = c.position
				if !c.nonarray {
					want.Position = fixture.Position(strings.Index(c.query, "v."+c.field) + 1)
				}
				for _, prepared := range []bool{false, true} {
					for _, queryMode := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(c.query, want)
						s.SelectRows = queryMode
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						run(s)
						unchanged()
					}
				}
			}
			for _, c := range []failure{
				{query: "UPDATE composite_diag_a.holder SET v.p=true", field: "p", destination: `"Pair One"`, source: "boolean"},
				{query: "INSERT INTO composite_diag_a.holder (k,v.p) VALUES (9,true)", field: "p", destination: `"Pair One"`, source: "boolean"},
				{query: "INSERT INTO composite_diag_a.holder (k,v.p) SELECT 9,true", field: "p", destination: `"Pair One"`, source: "boolean"},
				{query: "UPDATE composite_diag_a.holder SET v.p=ROW(9,'bad')::other_pair", field: "p", destination: `"Pair One"`, source: "other_pair"},
				{query: "UPDATE composite_diag_a.holder SET v.p=ROW(9,'bad')::composite_diag_b.other_pair", field: "p", destination: `"Pair One"`, source: "composite_diag_b.other_pair"},
				{query: "UPDATE composite_diag_a.holder SET v.d=true", field: "d", destination: "pair_domain", source: "boolean"},
				{query: "UPDATE composite_diag_a.holder SET v.p[1]=9", field: "p", destination: `"Pair One"`, nonarray: true},
				{query: "UPDATE composite_diag_a.holder SET k[1]=9", field: "k", destination: "integer", position: "36", nonarray: true},
				{query: "UPDATE composite_diag_a.holder SET v.d[1]=9", field: "d", destination: `"Pair One"`, nonarray: true},
			} {
				check(c)
			}
			run(fixture.Exec("SET search_path = composite_diag_b, composite_diag_a, public", 0))
			check(failure{query: `UPDATE composite_diag_a.holder SET v.p=ROW(9,'bad')::"Pair One"`, field: "p", destination: `composite_diag_a."Pair One"`, source: `"Pair One"`})
			check(failure{query: "UPDATE composite_diag_a.holder SET v.p=true", field: "p", destination: `composite_diag_a."Pair One"`, source: "boolean"})
			check(failure{query: "UPDATE composite_diag_a.holder SET v.p[1]=9", field: "p", destination: `composite_diag_a."Pair One"`, nonarray: true})
			// A failed analysis never poisons the same physical owner or a valid
			// held handle; its final write also proves target identity is retained.
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(held.SQL, 1, int64(7)))
			run(fixture.Select("SELECT k, ((v).p).i AS i FROM composite_diag_a.holder ORDER BY k", []string{"k", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(3)}}))
		})
	}
}
