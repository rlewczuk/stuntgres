package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func arraySearchStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE DOMAIN as_stored_array AS INT[]",
		"CREATE TABLE as_inputs(id INT,a as_stored_array,s INT,r INT,d INT[] DEFAULT (array_remove(ARRAY[1,2,1],1)),g INT GENERATED ALWAYS AS (array_position(a,s)) STORED,h BOOL GENERATED ALWAYS AS (a @> ARRAY[s]) STORED)",
		"CREATE INDEX as_position_idx ON as_inputs ((array_position(a,s)))",
		"CREATE TABLE as_guard(id INT,a INT[],CONSTRAINT as_guard_valid CHECK (array_position(a,1) IS NOT NULL))",
		"CREATE VIEW as_view AS SELECT id,array_position(a,s) AS p,array_positions(a,s) AS ps,array_remove(a,s) AS rm,array_replace(a,s,r) AS rp,a @> ARRAY[s] AS contains,g,h,d FROM as_inputs",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO as_inputs(id,a,s,r) VALUES (1,'[-2:2]={7,NULL,7,8,7}',7,9),(2,'{}',7,9),(3,NULL,NULL,NULL)", 3))
	run(fixture.Prepared(fixture.Exec("INSERT INTO as_guard VALUES ($1,$2)", 1, 1, "{1,2}")))
	return arraySearchSelect(b, "SELECT id,p,ps,rm,rp,contains,g,h,d FROM as_view ORDER BY id",
		[]string{"id", "p", "ps", "rm", "rp", "contains", "g", "h", "d"},
		[]string{"INT4", "INT4", "_INT4", "_INT4", "_INT4", "BOOL", "INT4", "BOOL", "_INT4"},
		[][]any{{int64(1), int64(-2), "{-2,0,2}", "[-2:-1]={NULL,8}", "[-2:2]={9,NULL,9,8,9}", true, int64(-2), true, "{2}"}, {int64(2), nil, "{}", "{}", "{}", false, nil, false, "{2}"}, {int64(3), nil, nil, nil, nil, nil, nil, nil, "{2}"}})
}

func TestReferenceArraySearchStoredConsumers(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := arraySearchStoredSetup(t, p, b, c)
		arraySearchBoth(t, p, b, c, check)
		stmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: check.Query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT count(array_position(a,s)) AS n,count(*) FILTER (WHERE a && ARRAY[s]) AS matched FROM as_inputs", []string{"n", "matched"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}),
			fixture.Select("SELECT array_position(a,s) AS p,count(*) AS n FROM as_inputs GROUP BY array_position(a,s) ORDER BY p", []string{"p", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(-2), int64(1)}, {nil, int64(2)}}),
			fixture.Select("SELECT l.id AS id FROM as_inputs AS l JOIN as_guard AS r ON l.a && r.a ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{}),
			fixture.Select("WITH choices AS (SELECT id,a,s FROM as_inputs) SELECT id FROM choices WHERE array_position(a,s) IS NOT NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT i.id FROM as_inputs AS i WHERE EXISTS (SELECT 1 FROM as_guard AS g WHERE array_position(i.a,i.s) IS NOT NULL AND g.id=1) ORDER BY i.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		} {
			arraySearchBoth(t, p, b, c, s)
		}
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "as_guard" violates check constraint "as_guard_valid"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (2, {2}).", "public", "as_guard", "as_guard_valid"
		s := fixture.ErrorDiagnostic("INSERT INTO as_guard VALUES (2,'{2}')", d)
		s.ErrorStage = "execute"
		arraySearchBoth(t, p, b, c, s)
		arraySearchBoth(t, p, b, c, fixture.Select("SELECT id FROM as_guard ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(arraySearchSelect(b, "UPDATE as_inputs SET r=10 WHERE a @> ARRAY[s] RETURNING id,array_replace(a,s,r) AS rp,g", []string{"id", "rp", "g"}, []string{"INT4", "_INT4", "INT4"}, [][]any{{int64(1), "[-2:2]={10,NULL,10,8,10}", int64(-2)}}))
		check.Rows[0][4] = "[-2:2]={10,NULL,10,8,10}"
		run(check)
		run(fixture.Exec("ALTER TABLE as_inputs RENAME COLUMN a TO choices", 0))
		run(fixture.Exec("ALTER TABLE as_inputs RENAME TO as_inputs_renamed", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		run(check)
		const definition = " SELECT id,\n    array_position(choices::integer[], s) AS p,\n    array_positions(choices::integer[], s) AS ps,\n    array_remove(choices::integer[], s) AS rm,\n    array_replace(choices::integer[], s, r) AS rp,\n    choices::integer[] @> ARRAY[s] AS contains,\n    g,\n    h,\n    d\n   FROM as_inputs_renamed;"
		arraySearchBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('as_view'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
	})
}

func TestReferenceArraySearchAuthorityAndControls(t *testing.T) {
	t.Run("authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"as_reader"})
		arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{"CREATE ROLE as_reader", "CREATE TABLE as_acl(id INT,a INT[],s INT,r INT,start INT)", "GRANT SELECT(id) ON as_acl TO as_reader"} {
				run(fixture.Exec(q, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO as_reader", 0))
			}
			for _, populated := range []bool{false, true} {
				if populated {
					run(fixture.Exec("INSERT INTO as_acl VALUES(1,NULL,NULL,NULL,NULL)", 1))
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION as_reader", 0))
				for _, q := range []string{
					"SELECT array_position(a,1) FROM as_acl",
					"SELECT array_position(NULL::int[],s) FROM as_acl",
					"SELECT array_position('{}'::int[],1,start) FROM as_acl",
					"SELECT array_replace(NULL::int[],1,r) FROM as_acl",
					"SELECT NULL::int[] && ARRAY[s] FROM as_acl",
					"SELECT ARRAY[1] @> ARRAY[1,r] FROM as_acl WHERE FALSE",
				} {
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table as_acl"))
					s.SelectRows, s.ErrorStage = true, "execute"
					arraySearchBoth(t, p, b, c, s)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
			run(fixture.Exec("GRANT SELECT(a,s,r,start) ON as_acl TO as_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION as_reader", 0))
			const authorizedSQL = "SELECT array_position(a,s,start) AS p,array_replace(a,s,r) AS rp FROM as_acl"
			authorized := arraySearchSelect(b, authorizedSQL, []string{"p", "rp"}, []string{"INT4", "_INT4"}, [][]any{{nil, nil}})
			arraySearchBoth(t, p, b, c, authorized)
			stmt, err := c.PrepareContext(p.Ctx, authorizedSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: authorizedSQL}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE SELECT(r) ON as_acl FROM as_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION as_reader", 0))
			denied := fixture.ErrorDiagnostic(authorizedSQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table as_acl"))
			denied.SelectRows, denied.ErrorStage = true, "execute"
			arraySearchBoth(t, p, b, c, denied)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT SELECT(r) ON as_acl TO as_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION as_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, authorized)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			arraySearchDiagnosticBoth(t, p, b, c, "SELECT array_replace(NULL::int[],1,missing) FROM as_acl", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "36"}, true)
		})
	})
	t.Run("atomicity_and_controls", func(t *testing.T) {
		arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			check := arraySearchStoredSetup(t, p, b, c)
			run(check)
			run(fixture.Exec("CREATE TABLE as_atomic(id INT,a INT[])", 0))
			run(fixture.Exec("INSERT INTO as_atomic VALUES(0,'{42}')", 1))
			unchanged := arraySearchSelect(b, "SELECT id,a FROM as_atomic ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(0), "{42}"}})
			q := "INSERT INTO as_atomic VALUES (1,array_remove($1::int[],1)),(2,array_remove($2::int[],1))"
			d := fixture.ControlsReferenceDiagnostic("0A000", "removing elements from multidimensional arrays is not supported")
			bad := fixture.ErrorDiagnostic(q, d, "{1,2}", "{{1}}")
			bad.ErrorStage = "execute"
			arraySearchBoth(t, p, b, c, bad)
			run(unchanged)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			run(unchanged)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{1,2}", "{1,3}"))
			unchanged.Rows = append(unchanged.Rows, []any{int64(1), "{2}"}, []any{int64(2), "{3}"})
			run(unchanged)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(ctx, "SELECT array_position(ARRAY[1],1)")
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("precanceled query", err)
			}
			if b.PG {
				return
			}
			rows, err = c.QueryContext(ctx, check.Query)
			if rows != nil {
				rows.Close()
				t.Fatal("native canceled owner published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("native canceled owner", err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				defer func() {
					for _, q := range []string{"DROP TABLE IF EXISTS as_inputs,as_guard,as_atomic CASCADE", "DROP DOMAIN IF EXISTS as_stored_array CASCADE"} {
						if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("emitted SQL replay", err)
				}
				pgCheck := fixture.ArrayReferenceSelect(check.Query, check.Columns, check.Types, check.Types, check.Rows, true)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
			}
			retained, err := c.QueryContext(p.Ctx, check.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if check.CheckMetadata != nil {
				check.CheckMetadata(t, retained)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
			arraySearchBoth(t, p, b, c, check)
			badImport := "CREATE TABLE as_staged(v INT[]); INSERT INTO as_staged VALUES ('{1}'); INSERT INTO as_staged VALUES (array_remove('{{1}}'::int[],1));"
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(badImport)), &d)
			run(unchanged)
			run(check)
			run(fixture.Error("SELECT v FROM as_staged", "42P01"))
		})
	})
}
