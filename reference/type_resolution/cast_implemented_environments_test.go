package reference_test

import (
	"database/sql/driver"
	"fmt"
	"io"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastImplementedEnvironments(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			for _, tc := range []struct {
				q, input        string
				settings, wants []string
			}{
				{"SELECT CAST((CAST(CAST($1 AS timestamp) AS timestamptz) AT TIME ZONE 'UTC') AS text) AS v", "2004-07-01 12:00:00", []string{"SET TIME ZONE 'UTC'", "SET TIME ZONE 'America/New_York'", "SET TIME ZONE 'UTC'"}, []string{"2004-07-01 12:00:00", "2004-07-01 16:00:00", "2004-07-01 12:00:00"}},
				{"SELECT CAST(CAST(CAST($1 AS timestamptz) AS timestamp) AS text) AS v", "2004-07-01 01:30:00+00", []string{"SET TIME ZONE 'UTC'", "SET TIME ZONE 'America/New_York'"}, []string{"2004-07-01 01:30:00", "2004-06-30 21:30:00"}},
				{"SELECT CAST(CAST(CAST($1 AS timestamptz) AS date) AS text) AS v", "2004-07-01 01:30:00+00", []string{"SET TIME ZONE 'UTC'", "SET TIME ZONE 'America/New_York'"}, []string{"2004-07-01", "2004-06-30"}},
				{"SELECT CAST(CAST(CAST($1 AS timestamptz) AS time) AS text) AS v", "2004-07-01 01:30:00+00", []string{"SET TIME ZONE 'UTC'", "SET TIME ZONE 'America/New_York'"}, []string{"01:30:00", "21:30:00"}},
				{"SELECT CAST(CAST(CAST($1 AS text) AS date) AS text) AS v", "02/03/2004", []string{"SET DateStyle='ISO, MDY'", "SET DateStyle='ISO, DMY'"}, []string{"2004-02-03", "2004-03-02"}},
				{"SELECT CAST(CAST($1 AS date) AS text) AS v", "2004-02-03", []string{"SET DateStyle='ISO, MDY'", "SET DateStyle='SQL, DMY'"}, []string{"2004-02-03", "03/02/2004"}},
				{"SELECT CAST(CAST($1 AS interval) AS text) AS v", "1 year 2 mons 3 days 04:05:06", []string{"SET IntervalStyle='postgres'", "SET IntervalStyle='iso_8601'"}, []string{"1 year 2 mons 3 days 04:05:06", "P1Y2M3DT4H5M6S"}},
				{"SELECT CAST(CAST(CAST($1 AS int4) AS bytea) AS text) AS v", "42", []string{"SET bytea_output='hex'", "SET bytea_output='escape'"}, []string{`\x0000002a`, `\000\000\000*`}},
				{"SELECT CAST(CAST($1 AS float8) AS text) AS v", "1.2345678901234567", []string{"SET extra_float_digits=0", "SET extra_float_digits=1"}, []string{"1.23456789012346", "1.2345678901234567"}},
			} {
				stmt, e := conn.PrepareContext(p.Ctx, tc.q)
				if e != nil {
					t.Fatal(e)
				}
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}
				for i, setting := range tc.settings {
					run(fixture.Exec(setting, 0))
					s := fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.wants[i]}}, tc.input)
					if i == 0 {
						run(s)
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					if i == 0 {
						s.Args = []any{nil}
						s.Rows = [][]any{{nil}}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
				if e = stmt.Close(); e != nil {
					t.Fatal(e)
				}
			}
			for _, q := range []string{"CREATE DOMAIN cast_env_bytes AS bytea", "CREATE TABLE cast_env_rows(n integer)", "INSERT INTO cast_env_rows VALUES(41),(42)", "CREATE TABLE cast_env_relation(n integer)", "CREATE TABLE cast_env_names(v regclass)", "INSERT INTO cast_env_names VALUES('cast_env_relation')"} {
				n := int64(0)
				if q == "INSERT INTO cast_env_rows VALUES(41),(42)" {
					n = 2
				}
				if q == "INSERT INTO cast_env_names VALUES('cast_env_relation')" {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			for _, mode := range []string{"hex", "escape"} {
				run(fixture.Exec("SET bytea_output='"+mode+"'", 0))
				leaf := `\x0000002a`
				array := `{"\\x0000002a",NULL}`
				record := `("\\x0000002a")`
				if mode == "escape" {
					leaf = `\000\000\000*`
					array = `{"\\000\\000\\000*",NULL}`
					record = `("\\000\\000\\000*")`
				}
				q := "SELECT CAST(CAST(CAST(n AS bytea) AS cast_env_bytes) AS text) AS d,CAST(ARRAY[CAST(n AS bytea),NULL::bytea] AS text) AS a,CAST(ROW(CAST(n AS bytea)) AS text) AS r FROM cast_env_rows WHERE n=42"
				s := fixture.Select(q, []string{"d", "a", "r"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{leaf, array, record}})
				run(s)
				run(fixture.Prepared(s))
			}
			q := "SELECT CAST(v AS text) AS v FROM cast_env_names"
			stmt, e := conn.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"cast_env_relation"}}))
			run(fixture.Exec("ALTER TABLE cast_env_relation RENAME TO cast_env_renamed", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"cast_env_renamed"}}))
			if !b.PG {
				// Keep the public borrowed raw handle throughout. Deferred rows must retain
				// the operation's output environment, not consult the later session setting.
				err = conn.Raw(func(raw any) error {
					exec := raw.(driver.ExecerContext)
					query := raw.(driver.QueryerContext)
					if _, e := exec.ExecContext(p.Ctx, "SET bytea_output='hex'", nil); e != nil {
						return e
					}
					rows, e := query.QueryContext(p.Ctx, "SELECT CAST(CAST(n AS bytea) AS text) AS v FROM cast_env_rows ORDER BY n", nil)
					if e != nil {
						return e
					}
					defer rows.Close()
					if _, e = exec.ExecContext(p.Ctx, "SET bytea_output='escape'", nil); e != nil {
						return e
					}
					values := make([]driver.Value, 1)
					for _, expected := range []string{`\x00000029`, `\x0000002a`} {
						if e = rows.Next(values); e != nil {
							return e
						}
						if values[0] != expected {
							return fmt.Errorf("retained cast output %v want %s", values[0], expected)
						}
					}
					if e = rows.Next(values); e != io.EOF {
						return fmt.Errorf("retained exhaustion %v", e)
					}
					return rows.Close()
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
