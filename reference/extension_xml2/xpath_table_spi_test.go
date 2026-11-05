package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableSPI(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		xtExec(t, p, b, c, "CREATE TABLE xt_audit(id integer)")
		for i, route := range []string{"direct", "prepared"} {
			t.Run("read_write_final_select_"+route, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM xt_audit", int64(i)))
				criteria := "false; INSERT INTO xt_audit VALUES (1); SELECT 9, '<doc><a>1</a></doc>'"
				s := fixture.Select(xtQuery("id", "doc", "xt_src", "/doc/a", criteria, "id integer,a integer"), []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(1)}})
				s.Prepared = route == "prepared"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM xt_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		}
		for _, tc := range []struct {
			name, criteria, path, definitions string
			want                              fixture.Diagnostic
		}{
			{"nonselect", "false; INSERT INTO xt_audit VALUES (2)", "/doc/a", "id integer,a integer", xml2Error("XX000", "xpath_table: SPI execution failed for query SELECT id, doc FROM xt_src WHERE false; INSERT INTO xt_audit VALUES (2)")},
			{"cast_after_write", "false; INSERT INTO xt_audit VALUES (3); SELECT 9, '<doc><a>x</a></doc>'", "/doc/a", "id integer,a integer", xml2Error("22P02", `invalid input syntax for type integer: "x"`)},
			{"compile_after_write", "false; INSERT INTO xt_audit VALUES (4); SELECT 9, '<doc/>'", "[", "id integer,a integer", xml2Compile("Invalid expression")},
			{"width_after_write", "false; INSERT INTO xt_audit VALUES (5); SELECT 9, '<doc/>', 3", "/doc/a", "id integer,a integer", xtWidthDiagnostic()},
		} {
			xtFailure(t, p, b, c, tc.name, xtQuery("id", "doc", "xt_src", tc.path, tc.criteria, tc.definitions), tc.want, false)
			xtBoth(t, p, b, c, tc.name+"_statement_rollback", fixture.Select("SELECT id FROM xt_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		xtExec(t, p, b, c, "CREATE TABLE xt_late(seq integer,id integer,doc text)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_late VALUES(1,1,'<doc><a>x</a></doc>'),(2,0,'<doc/>')", 2))
		for _, tc := range []struct{ name, key, path string }{
			{"source_before_cast", "id", "/doc/a"},
			{"source_before_compile", "id", "["},
			{"source_before_shape", "id,id", "/doc/a"},
		} {
			// The source evaluates its second row before any XML or destination IO.
			xtFailure(t, p, b, c, tc.name, xtQuery(tc.key, "doc", "(SELECT id, CASE WHEN seq=2 THEN (1/id)::text ELSE doc END AS doc FROM xt_late ORDER BY seq) AS s", tc.path, "true", "id integer,a integer"), xtStatement(xml2Error("22012", "division by zero"), "SELECT "+tc.key+", doc FROM (SELECT id, CASE WHEN seq=2 THEN (1/id)::text ELSE doc END AS doc FROM xt_late ORDER BY seq) AS s WHERE true"), false)
		}
		t.Run("held_failure_then_healthy", func(t *testing.T) {
			q := "SELECT * FROM xpath_table($1::text,$2::text,$3::text,$4::text,$5::text) AS x(id integer,a integer) ORDER BY id,a"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				name, path, criteria string
				want                 fixture.Diagnostic
			}{
				{"cast", "/doc/a", "false; INSERT INTO xt_audit VALUES (6); SELECT 9, '<doc><a>x</a></doc>'", xml2Error("22P02", `invalid input syntax for type integer: "x"`)},
				{"compile", "[", "false; INSERT INTO xt_audit VALUES (7); SELECT 9, '<doc/>'", xml2Compile("Invalid expression")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					bad := fixture.ErrorDiagnostic(q, tc.want, "id", "doc", "xt_src", tc.path, tc.criteria)
					bad.SelectRows, bad.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, h, b.Recorder, bad)
					fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(20)}}, "id", "doc", "xt_src", "/doc/a", "id=1"))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM xt_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		})
		t.Run("admitted_source_cancellation", func(t *testing.T) {
			criteria := "false; INSERT INTO xt_audit VALUES (8); SELECT 9, pg_sleep(600)::text"
			q := xtQuery("id", "doc", "xt_src", "/doc/a", criteria, "id integer,a text")
			source := "SELECT id, doc FROM xt_src WHERE " + criteria
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, prepared := range []bool{false, true} {
				fixture.SleepActiveCancelWithContext(t, p, b, c, q, "SQL statement \""+source+"\"", nil, func(ctx context.Context) fixture.RelationOutcome {
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM xt_audit ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				xml2Both(t, p, b, c, fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "7"}}, "id", "doc", "xt_src", "/doc/b", "id=1"))
			}
		})
	})
}
