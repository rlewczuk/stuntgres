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

func cduRetained(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	used := make([]bool, len(want))
	count := 0
	for rows.Next() {
		var cells [7]any
		if err := rows.Scan(&cells[0], &cells[1], &cells[2], &cells[3], &cells[4], &cells[5], &cells[6]); err != nil {
			t.Fatal(err)
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

func TestReferenceInfoColumnDomainUsageNativeControls(t *testing.T) {
	for _, phase := range []string{"same_engine_restore", "independent_target_restore", "canceled_reads"} {
		t.Run(phase, func(t *testing.T) {
			cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				cduSeed(run)
				s := cduProjection(b.PG, "table_schema='cdu'", cduSeedRows(database))
				held := cduHold(t, p, c, s.Query)
				cduCheck(t, p, c, r, held, s)
				cduIdentity(t, p, b, c, r)
				run(cduProjection(b.PG, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(database)))
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
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("canceled query=%v", err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
						}
					}
					cduCheck(t, p, c, r, held, s)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
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
					cduMetadata(t, columns, false)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(cduProjection(false, "table_schema='cdu'", nil))
					run(cduProjection(false, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(database)))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					run(s)
					run(fixture.Prepared(s))
					cduIdentity(t, p, b, c, r)
					cduRetained(t, retained, cduSeedRows(database))
					cduMetadata(t, columns, false)
					return
				}
				target := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var targetDatabase string
				if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&targetDatabase); err != nil {
					t.Fatal(err)
				}
				targetConn, connErr := target.Memory.Conn(target.Ctx)
				if connErr != nil {
					t.Fatal(connErr)
				}
				defer targetConn.Close()
				checkTarget := func() {
					step := cduProjection(false, "table_schema='cdu'", cduSeedRows(targetDatabase))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(step))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, cduProjection(false, "table_schema='information_schema' AND table_name='column_domain_usage'", cduSelfRows(targetDatabase)))
					cduIdentity(t, target, b, targetConn, target.MemoryRecord)
				}
				checkTarget()
				bad := "CREATE SCHEMA cdu_staged; CREATE DOMAIN cdu_staged.d AS integer; CREATE TABLE cdu_staged.t(v cdu_staged.d); INSERT INTO cdu_staged.t VALUES(1),('bad');"
				err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
				fixture.AssertDiagnostic(t, err, func() *fixture.Diagnostic {
					d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
					d.Position = strconv.Itoa(strings.Index(bad, "'bad'") + 1)
					return &d
				}())
				checkTarget()
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname='cdu_staged'", []string{"nspname"}, []string{"NAME"}, nil))
				ctx, cancel := context.WithCancel(target.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled Restore=%v", err)
				}
				checkTarget()
			})
		})
	}
}
