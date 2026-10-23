package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotMaterializedRefresh(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			for _, atomic := range []bool{false, true} {
				name := "direct/plain/no-source"
				if prepared {
					name = "held/plain/no-source"
				}
				if nested {
					name += "/nested"
				}
				if atomic {
					name += "/atomic"
				}
				t.Run(name, func(t *testing.T) {
					snapshotRemedyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
						query := "SELECT pg_current_snapshot() AS s"
						if atomic {
							fixture.LockExec(t, p.Ctx, o, "CREATE TABLE snapshot_refresh_data(id integer,den integer)", 0)
							fixture.LockExec(t, p.Ctx, o, "INSERT INTO snapshot_refresh_data VALUES(1,1),(2,1)", 2)
							query = "SELECT id,pg_current_snapshot() AS s,10/den AS quotient FROM snapshot_refresh_data"
						}
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						id := identityID(t, p, a)
						snapshotBump(t, p, bumper, id)
						if nested {
							fixture.LockExec(t, p.Ctx, o, "CREATE VIEW snapshot_refresh_input AS "+query, 0)
							query = "SELECT s FROM snapshot_refresh_input"
							if atomic {
								query = "SELECT id,s,quotient FROM snapshot_refresh_input"
							}
						}
						created := int64(1)
						if atomic {
							created = 2
						}
						fixture.LockExec(t, p.Ctx, o, "CREATE MATERIALIZED VIEW snapshot_refresh AS "+query, created)
						read := func(visible bool) [][]any {
							q := "SELECT s FROM snapshot_refresh"
							columns, types := []string{"s"}, []string{snapshotType(b, "PG_SNAPSHOT")}
							count, snapshotCol := 1, 0
							if atomic {
								q = "SELECT id,s,quotient FROM snapshot_refresh ORDER BY id"
								columns = []string{"id", "s", "quotient"}
								types = []string{"INT4", snapshotType(b, "PG_SNAPSHOT"), "INT4"}
								count, snapshotCol = 2, 1
							}
							out := snapshotRemedyRead(o.QueryContext(p.Ctx, q))
							snapshotRemedyShape(t, out, columns, types, count)
							s, ok := out.values[0][snapshotCol].(string)
							if !ok {
								t.Fatalf("stored snapshot %T", out.values[0][snapshotCol])
							}
							snapshotRemedyPeer(t, p, b, o, s, id, visible)
							want := [][]any{{s}}
							if atomic {
								want = [][]any{{int64(1), s, int64(10)}, {int64(2), s, int64(10)}}
							}
							if !reflect.DeepEqual(out.values, want) {
								t.Fatalf("stored rows %v want %v", out.values, want)
							}
							// Exercise the existing typed/raw/ScanType helper through a
							// computed expression, not stored-column nullability guesses.
							typedRows := make([][]any, count)
							for i := range typedRows {
								typedRows[i] = []any{s}
							}
							snapshotBoth(t, p, b, o, snapshotTypedStep(b, "SELECT (s::text)::pg_snapshot AS s FROM snapshot_refresh", []string{"s"}, []string{"PG_SNAPSHOT"}, typedRows))
							return out.values
						}
						creation := read(false)
						const refresh = "REFRESH MATERIALIZED VIEW snapshot_refresh"
						var executor fixture.Executor = o
						if prepared {
							held, err := o.PrepareContext(p.Ctx, refresh)
							if err != nil {
								t.Fatal("prepare REFRESH must not populate", err)
							}
							t.Cleanup(func() { held.Close() })
							executor = fixture.HeldStatement{Executor: o, Statement: held, SQL: refresh}
						}
						fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(refresh, 0))
						published := read(true)
						snapshotCol := 0
						if atomic {
							snapshotCol = 1
						}
						snapshotVisible(t, p, b, o, id, creation[0][snapshotCol].(string), false)
						if !atomic {
							return
						}
						fixture.LockExec(t, p.Ctx, o, "UPDATE snapshot_refresh_data SET den=0 WHERE id=2", 1)
						failure := fixture.ErrorDiagnostic(refresh, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
						failure.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, failure)
						// The exact last published snapshot bytes and BOTH old rows
						// survive. No evaluation-order assumption is made.
						if after := read(true); !reflect.DeepEqual(after, published) {
							t.Fatalf("failed refresh replaced contents: %v want %v", after, published)
						}
						snapshotBoth(t, p, b, o, fixture.Select("SELECT id,den FROM snapshot_refresh_data ORDER BY id", []string{"id", "den"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
						// A second active peer distinguishes successful retry's fresh
						// snapshot from the previously published successful refresh.
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						retryPeer := identityID(t, p, a)
						snapshotBump(t, p, bumper, retryPeer)
						fixture.LockExec(t, p.Ctx, o, "UPDATE snapshot_refresh_data SET den=1 WHERE id=2", 1)
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(refresh, 0))
						recovered := read(true)
						recoveredS := recovered[0][1].(string)
						snapshotRemedyPeer(t, p, b, o, recoveredS, retryPeer, false)
						if recoveredS == published[0][1] {
							t.Fatal("successful retry retained old population snapshot")
						}
						fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
						snapshotVisible(t, p, b, o, retryPeer, recoveredS, false)
					})
				})
			}
		}
	}
}
