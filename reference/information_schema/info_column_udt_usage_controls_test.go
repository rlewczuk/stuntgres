package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func cuRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	count := 0
	for rows.Next() {
		var cells [7]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6]); err != nil {
			t.Fatal(err)
		}
		for i := range cells {
			cells[i] = fixture.Normalize(cells[i])
		}
		matched := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(cells[:], expected) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			t.Fatalf("unexpected retained row %v", cells)
		}
		count++
	}
	if rows.Err() != nil || count != len(want) {
		t.Fatalf("retained count=%d want=%d error=%v", count, len(want), rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceInfoColumnUdtUsageNativeControls(t *testing.T) {
	for _, phase := range []string{"same_engine_restore", "independent_target_restore", "canceled_reads"} {
		t.Run(phase, func(t *testing.T) {
			cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				cuOwnedSeed(run, "cu_scope")
				run(fixture.Exec("INSERT INTO cu_scope.t VALUES('x','x','x')", 1))
				s := cuProjection(b.PG, "table_schema='cu_scope'", cuOwnedRows(database, "cu_scope"))
				held := cduHold(t, p, c, s.Query)
				cuCheck(t, p, c, r, held, s)
				cuIdentity(t, p, b, c, r)
				run(cuProjection(b.PG, "table_schema='information_schema' AND table_name='column_udt_usage'", cuSelfRows(database)))
				run(fixture.Exec("SET SESSION AUTHORIZATION cu_type_owner", 0))
				run(cuProjection(b.PG, "table_schema='cu_scope'", cuOwnedRows(database, "cu_scope")[:2]))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				if b.PG {
					return
				} // PostgreSQL has no process-local native controls.
				if phase == "canceled_reads" {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, x := range []fixture.Executor{c, held} {
						rows, err := x.QueryContext(ctx, s.Query)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled metadata published rows")
						}
						cuCanceled(t, err)
					}
					cuCheck(t, p, c, r, held, s)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "CREATE VIEW information_schema.column_udt_usage") || strings.Contains(dump.String(), `CREATE VIEW "information_schema"."column_udt_usage"`) {
					t.Fatal("builtin source serialized as user view")
				}
				if phase == "same_engine_restore" {
					retained, err := b.DB.QueryContext(p.Ctx, s.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					columns, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					cuMetadata(t, columns, false)
					for range 2 {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						empty := cuProjection(false, "table_schema='cu_scope'", nil)
						cuCheck(t, p, c, r, cduHold(t, p, c, empty.Query), empty)
						run(cuProjection(false, "table_schema='information_schema' AND table_name='column_udt_usage'", cuSelfRows(database)))
						cuIdentity(t, p, b, c, r)
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						cuCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
						cuControlData(t, p, c, r)
						cuControlOwners(t, p, c, r, database)
						cuIdentity(t, p, b, c, r)
					}
					cuRetained(t, retained, cuOwnedRows(database, "cu_scope"))
					cuMetadata(t, columns, false)
					return
				}
				// A fresh fixture owns a distinct public Driver and recording pool.
				target := fixture.NewFixture(t, false, true)
				for _, role := range cuRoles {
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE "+role, 0))
				}
				for _, query := range []string{"CREATE TABLE public.cu_spacer(v integer)", "CREATE DOMAIN public.cu_spacer_domain AS text"} {
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(query, 0))
				}
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var targetDatabase string
				if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&targetDatabase); err != nil {
					t.Fatal(err)
				}
				targetConn, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer targetConn.Close()
				checkTarget := func() {
					step := cuProjection(false, "table_schema='cu_scope'", cuOwnedRows(targetDatabase, "cu_scope"))
					cuCheck(t, target, targetConn, target.MemoryRecord, cduHold(t, target, targetConn, step.Query), step)
					fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, cuProjection(false, "table_schema='information_schema' AND table_name='column_udt_usage'", cuSelfRows(targetDatabase)))
					cuIdentity(t, target, b, targetConn, target.MemoryRecord)
					cuControlData(t, target, targetConn, target.MemoryRecord)
					cuControlOwners(t, target, targetConn, target.MemoryRecord, targetDatabase)
					cuCheck(t, p, c, r, held, s)
					cuControlData(t, p, c, r)
				}
				checkTarget()
				var second bytes.Buffer
				if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(second.Bytes())); err != nil {
					t.Fatal(err)
				}
				checkTarget()
				bad := "CREATE SCHEMA cu_staged; CREATE DOMAIN cu_staged.d AS integer; CREATE TABLE cu_staged.t(v cu_staged.d); INSERT INTO cu_staged.t VALUES(1),('bad');"
				err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
				d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				d.Position = strconv.Itoa(strings.Index(bad, "'bad'") + 1)
				fixture.AssertDiagnostic(t, err, &d)
				checkTarget()
				fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Select("SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname='cu_staged'", []string{"nspname"}, []string{"NAME"}, nil))
				fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, cuProjection(false, "table_schema='cu_staged'", nil))
				ctx, cancel := context.WithCancel(target.Ctx)
				cancel()
				cuCanceled(t, stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())))
				checkTarget()
			})
		})
	}
}

func cuCanceled(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
	}
}

func cuControlData(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	s := fixture.Select("SELECT x::text AS x,y::text AS y,z::text AS z FROM cu_scope.t", []string{"x", "y", "z"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"x", "x", "x"}})
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func cuControlOwners(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, database string) {
	t.Helper()
	for _, x := range []struct {
		role string
		rows [][]any
	}{{"cu_type_owner", cuOwnedRows(database, "cu_scope")[:2]}, {"cu_parent_owner", cuOwnedRows(database, "cu_scope")[2:]}, {"cu_child_owner", nil}, {"cu_table_owner", nil}} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET SESSION AUTHORIZATION "+x.role, 0))
		fixture.RunStep(t, p.Ctx, c, r, cuProjection(false, "table_schema='cu_scope'", x.rows))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}
}
