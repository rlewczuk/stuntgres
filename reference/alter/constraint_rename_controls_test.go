package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameNativeControls(t *testing.T) {
	for _, k := range constraintRenameKinds {
		t.Run(k.kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t("+k.declaration+")", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				run(fixture.Exec("COMMENT ON TABLE t IS 'table comment'", 0))
				run(fixture.Exec("COMMENT ON COLUMN t.k IS 'column comment'", 0))
				query := "SELECT k,payload FROM t"
				held, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
				var retained *sql.Rows
				if !b.PG {
					retained, err = held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
				}
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if strings.Count(dump.String(), `CONSTRAINT "new"`) != 1 || strings.Contains(dump.String(), "CREATE INDEX") || strings.Contains(dump.String(), `CONSTRAINT "old"`) {
						t.Fatal("noncanonical renamed definition", dump.String())
					}
					for range 2 {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
							t.Fatal(err)
						}
						var again bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
							t.Fatal("noncanonical roundtrip", err, again.String())
						}
					}
					freshDriver := stuntgres.NewDriver()
					connector, err := freshDriver.OpenConnector("constraint-rename-restore")
					if err != nil {
						t.Fatal(err)
					}
					fresh := sql.OpenDB(connector)
					defer fresh.Close()
					if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
					freshHeld, err := fresh.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer freshHeld.Close()
					rows, err := freshHeld.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 2 || types[0].Name() != "k" || types[0].DatabaseTypeName() != "INT4" || types[1].Name() != "payload" || types[1].DatabaseTypeName() != "TEXT" {
						t.Fatal("fresh metadata", types, err)
					}
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					var value int64
					var text string
					if err := rows.Scan(&value, &text); err != nil || value != 1 || text != "keep" {
						t.Fatal(value, text, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					rows.Close()
					q, d := constraintRenameViolation(k, "new")
					_, err = fresh.ExecContext(p.Ctx, q)
					fixture.AssertDiagnostic(t, err, &d)
					bad := dump.String() + q + ";\n"
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
					fixture.AssertDiagnostic(t, err, &d)
					var unchanged bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || unchanged.String() != dump.String() {
						t.Fatal("failed restore changed destination", err)
					}
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("canonical PG replay", err, dump.String())
						}
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("DROP TABLE t", 0))
					}
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, fixture.Select(query, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				q, d := constraintRenameViolation(k, "new")
				run(fixture.ErrorDiagnostic(q, d))
				run(fixture.Select("SELECT obj_description('t'::regclass),col_description('t'::regclass,1)", []string{"obj_description", "col_description"}, []string{"TEXT", "TEXT"}, [][]any{{"table comment", "column comment"}}))
				if retained != nil {
					if !retained.Next() {
						t.Fatal(retained.Err())
					}
					var k int64
					var payload string
					if err := retained.Scan(&k, &payload); err != nil || k != 1 || payload != "keep" {
						t.Fatal(k, payload, err)
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal(retained.Err())
					}
				}
			})
		})
	}
}
