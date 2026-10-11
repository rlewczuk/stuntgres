package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedQueued(t *testing.T) {
	for _, action := range []string{"rename-commit", "rename-rollback", "drop-commit"} {
		t.Run("Queued/"+action, func(t *testing.T) {
			p := fixture.NewScenario(t)
			if p.Postgres == nil {
				return
			} // O: real lock queue and captured OID.
			a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
			fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s START 10")
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 10)
			var oid int64
			if err := a.QueryRowContext(p.Ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			held, err := b.PrepareContext(p.Ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ap, bp := sequencePreparedPID(t, p, a), sequencePreparedPID(t, p, b)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := "ALTER SEQUENCE s RENAME TO identity"
			if action == "drop-commit" {
				query = "DROP SEQUENCE s"
			}
			fixture.LockExec(t, p.Ctx, tx, query, 0)
			await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := held.QueryContext(context.WithValue(ctx, fixture.RecorderKey{}, p.PostgresRecord))
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			fixture.SequenceWaitBehind(t, p.Ctx, bp, ap, oid)
			if action == "rename-rollback" {
				err = tx.Rollback()
			} else {
				err = tx.Commit()
			}
			if err != nil {
				t.Fatal(err)
			}
			outcome := await()
			if action == "drop-commit" {
				if outcome.Rows != nil {
					outcome.Rows.Close()
					t.Fatal("failed original-OID opening returned cursor")
				}
				want := fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
				fixture.AssertDiagnostic(t, outcome.Err, &want)
			} else {
				if outcome.Err != nil {
					t.Fatal(outcome.Err)
				}
				fixture.SequenceRows(t, outcome.Rows, "nextval", 11)
				sequencePreparedRecorded(t, fixture.Backend{Recorder: p.PostgresRecord, PG: true}, fixture.Step{Query: "SELECT nextval('s')", Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(11)}}})
			}
			if action != "rename-rollback" {
				var v int64
				err := held.QueryRowContext(p.Ctx).Scan(&v)
				want := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
				want.Position = "16"
				fixture.AssertDiagnostic(t, err, &want)
				if action == "rename-commit" {
					fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('identity')", 12)
				}
			} else {
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 12)
			}
		})
	}
}
