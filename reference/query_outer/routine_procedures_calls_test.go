package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineProceduresSQLBodies(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "fresh_prepared"
		}
		t.Run(route, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c,
					"CREATE TABLE pc_log(v integer)",
					"CREATE PROCEDURE pc_write(IN delta integer,OUT answer integer,OUT label text) LANGUAGE SQL AS $$UPDATE pc_log SET v=v+delta; SELECT v AS misleading,'written'::text AS body_label FROM pc_log$$",
					"CREATE PROCEDURE pc_standard(IN delta integer,OUT answer integer) BEGIN ATOMIC UPDATE pc_log SET v=v+delta; SELECT v FROM pc_log; END",
					"CREATE PROCEDURE pc_noout(IN delta integer) LANGUAGE SQL AS $$UPDATE pc_log SET v=v+delta; SELECT 999$$",
					"CREATE PROCEDURE pc_returning(IN delta integer,OUT answer integer) LANGUAGE SQL AS $$UPDATE pc_log SET v=v+delta RETURNING v$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pc_log VALUES(10)", 1))
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				for _, tc := range []struct {
					q            string
					names, types []string
					values       [][]any
					stored       int64
				}{
					{"CALL pc_write(5,NULL,NULL)", []string{"answer", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(15), "written"}}, 15},
					{"CALL pc_standard(2,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(17)}}, 17},
					{"CALL pc_noout(3)", []string{}, []string{}, [][]any{}, 20},
					{"CALL pc_returning(4,NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(24)}}, 24},
				} {
					run(proceduresStep(b, tc.q, tc.names, tc.types, tc.values))
					resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_log", []string{"v"}, []string{"INT4"}, [][]any{{tc.stored}})
				}
				// Exec has CALL's completion count, never the body's UPDATE count.
				run(fixture.Exec("CALL pc_write(0,NULL,NULL)", 0))
				run(fixture.Exec("CALL pc_noout(0)", 0))
				q := "CALL pc_write($1::integer,NULL,NULL)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(24)}})
				for i, delta := range []int{2, 3} {
					want := []int64{26, 29}[i]
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, proceduresStep(b, q, []string{"answer", "label"}, []string{"INT4", "TEXT"}, [][]any{{want, "written"}}, delta))
				}
				resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(29)}})
				proceduresHeld(t, p, b, c, "CALL pc_noout($1::integer)", []string{}, []string{}, [][]any{{2}, {-2}}, [][][]any{{}, {}})
				resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(29)}})
			})
		})
	}
	t.Run("output_shapes", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE TYPE pc_pair AS (key integer,val text)",
				"CREATE PROCEDURE pc_inout(INOUT n bigint) LANGUAGE SQL AS 'SELECT n+1'",
				"CREATE PROCEDURE pc_unnamed(OUT integer,OUT text) LANGUAGE SQL AS $$SELECT 4,'label'::text$$",
				"CREATE PROCEDURE pc_composite(OUT item pc_pair) LANGUAGE SQL AS $$SELECT ROW(4,'pair')::pc_pair$$",
				"CREATE PROCEDURE pc_convert(OUT answer bigint) LANGUAGE SQL AS 'SELECT 7::integer'",
				"CREATE PROCEDURE pc_empty(OUT answer integer,OUT label text) LANGUAGE SQL AS $$SELECT 7,'x'::text WHERE false$$",
				"CREATE PROCEDURE pc_null(OUT answer integer) LANGUAGE SQL AS 'SELECT NULL::integer'")
			proceduresBoth(t, p, b, c, "CALL pc_inout(9)", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}})
			proceduresBoth(t, p, b, c, "CALL pc_inout(NULL)", []string{"n"}, []string{"INT8"}, [][]any{{nil}})
			proceduresBoth(t, p, b, c, "CALL pc_unnamed(NULL,NULL)", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "label"}})
			proceduresBoth(t, p, b, c, "CALL pc_composite(NULL)", []string{"item"}, []string{""}, [][]any{{"(4,pair)"}})
			proceduresBoth(t, p, b, c, "CALL pc_convert(NULL)", []string{"answer"}, []string{"INT8"}, [][]any{{int64(7)}})
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				resultsError(t, p, c, "CALL pc_empty(NULL,NULL)", prepared, stage, proceduresDiagnostic("XX000", "procedure returned null record", 0))
			}
			proceduresBoth(t, p, b, c, "CALL pc_null(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{nil}})
		})
	})
	t.Run("shape_rejection_and_revalidation", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE TABLE pc_shape_effect(v integer)", "CREATE TABLE pc_shape_source(a integer,b integer)")
			want := proceduresDiagnostic("42P13", "return type mismatch in function declared to return record", 0)
			want.Detail = "Final statement returns too few columns."
			want.Context = `SQL function "pc_shape_bad"`
			resultsError(t, p, c, "CREATE PROCEDURE pc_shape_bad(OUT a integer,OUT b integer) LANGUAGE SQL AS 'INSERT INTO pc_shape_effect VALUES(1);SELECT 1'", false, "execute", want)
			proceduresCount(t, p, b, c, "pc_shape_effect", 0)
			proceduresCount(t, p, b, c, "pg_proc WHERE proname='pc_shape_bad'", 0)
			resultsExec(t, p, b, c, "CREATE PROCEDURE pc_shape_bad(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 1,2'",
				"CREATE PROCEDURE pc_shape_live(IN x integer,OUT a integer,OUT b integer) LANGUAGE SQL AS 'INSERT INTO pc_shape_effect VALUES(x);SELECT a,b FROM pc_shape_source'")
			proceduresBoth(t, p, b, c, "CALL pc_shape_bad(NULL,NULL)", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			q := "CALL pc_shape_live($1::integer,NULL,NULL)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			proceduresCount(t, p, b, c, "pc_shape_effect", 0)
			resultsExec(t, p, b, c, "ALTER TABLE pc_shape_source ALTER COLUMN b TYPE text USING b::text")
			want.Detail = "Final statement returns text instead of integer at column 2."
			want.Context = `SQL function "pc_shape_live" statement 2`
			resultsError(t, p, c, q, true, "execute", want, 9)
			rows, err := stmt.QueryContext(p.Ctx, 9)
			if rows != nil {
				rows.Close()
				t.Fatal("invalid shape published CALL row")
			}
			fixture.AssertDiagnostic(t, err, &want)
			proceduresCount(t, p, b, c, "pc_shape_effect", 0)
			resultsExec(t, p, b, c, "ALTER TABLE pc_shape_source ALTER COLUMN b TYPE integer USING b::integer")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pc_shape_source VALUES(4,5)", 1))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, proceduresStep(b, q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(5)}}, 4))
			resultsStoredBoth(t, p, b, c, "SELECT v FROM pc_shape_effect", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}})
		})
	})
}

func TestReferenceRoutineProceduresMetadataCatalog(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		proceduresMixSetup(t, p, b, c)
		proceduresMixCheck(t, p, b, c)
		q := "SELECT proname::text AS name,prokind::text AS kind,pronargs::integer AS inputs,proargtypes::text AS inputtypes,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,pronargdefaults::integer AS defaults,proretset AS sets,prorettype::bigint AS result,provariadic::bigint AS variadic FROM pg_proc WHERE proname IN ('pc_mix','pc_out','pc_noout','pc_array') ORDER BY proname"
		names := []string{"name", "kind", "inputs", "inputtypes", "alltypes", "modes", "names", "defaults", "sets", "result", "variadic"}
		types := []string{"TEXT", "TEXT", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "INT8", "INT8"}
		profiles := []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, {}, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata, {}, {}}
		resultsBothMetadata(t, p, b, c, q, names, types, [][]any{
			{"pc_array", "p", int64(1), "1007", "{1007,1007}", "{o,v}", "{answer,xs}", int64(0), false, int64(2249), int64(23)},
			{"pc_mix", "p", int64(3), "23 20 23", "{23,23,20,25,23}", "{o,i,b,o,i}", "{first,x,total,note,bonus}", int64(1), false, int64(2249), int64(0)},
			{"pc_noout", "p", int64(1), "23", nil, nil, "{delta}", int64(0), false, int64(2278), int64(0)},
			{"pc_out", "p", int64(0), "", "{23}", "{o}", "{answer}", int64(0), false, int64(2249), int64(0)},
		}, profiles)
		proceduresDefaultCountsCheck(t, p, b, c)
		resultsExec(t, p, b, c, "CREATE EXTENSION amcheck",
			"CREATE PROCEDURE pc_defaults_pl(INOUT n integer DEFAULT 3) LANGUAGE plpgsql AS $$BEGIN RETURN; END$$",
			"CREATE FUNCTION pc_defaults_sql(x integer DEFAULT 1,y integer DEFAULT 2) RETURNS integer LANGUAGE SQL AS 'SELECT x+y'")
		resultsBothMetadata(t, p, b, c,
			"SELECT proname::text AS name,pronargdefaults::integer AS defaults FROM pg_proc WHERE proname IN ('verify_heapam','pc_defaults_pl','pc_defaults_sql') ORDER BY proname",
			[]string{"name", "defaults"}, []string{"TEXT", "INT4"},
			[][]any{{"pc_defaults_pl", int64(1)}, {"pc_defaults_sql", int64(2)}, {"verify_heapam", int64(5)}},
			[]resultsNativeColumn{resultsTextMetadata, {}})
		resultsBothMetadata(t, p, b, c,
			"SELECT attnum::integer AS number,atttypid::bigint AS type,attnotnull AS required FROM pg_attribute WHERE attrelid=1255 AND attname='pronargdefaults'",
			[]string{"number", "type", "required"}, []string{"INT4", "INT8", "BOOL"},
			[][]any{{int64(18), int64(21), true}}, []resultsNativeColumn{{}, {}, resultsNonNullMetadata})
		resultsExec(t, p, b, c, "CREATE PROCEDURE pc_meta_pl(IN x integer,INOUT y integer,OUT note text) LANGUAGE plpgsql AS $$BEGIN y:=y+x;note:='done';END$$")
		proceduresBoth(t, p, b, c, "CALL pc_meta_pl(4,10,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(14), "done"}})
		proceduresHeld(t, p, b, c, "CALL pc_meta_pl($1::integer,$2::integer,NULL)", []string{"y", "note"}, []string{"INT4", "TEXT"}, [][]any{{4, 10}, {8, 20}, {4, nil}}, [][][]any{{{int64(14), "done"}}, {{int64(28), "done"}}, {{nil, "done"}}})
		// Consume a detached result after a later call and same-shape replacement.
		rows, err := c.QueryContext(p.Ctx, "CALL pc_out(NULL)")
		if err != nil {
			t.Fatal(err)
		}
		proceduresMetadata(b, []string{"answer"}, []string{"INT4"})(t, rows)
		if b.PG {
			// pq streams; finish the row before issuing another operation on its owner.
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var v int64
			if err := rows.Scan(&v); err != nil || v != 7 {
				t.Fatal(v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		} else {
			defer rows.Close()
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 8'")
			proceduresBoth(t, p, b, c, "CALL pc_out(NULL)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(8)}})
			proceduresMetadata(b, []string{"answer"}, []string{"INT4"})(t, rows)
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var v int64
			if err := rows.Scan(&v); err != nil || v != 7 {
				t.Fatal(v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
		}
	})
}
