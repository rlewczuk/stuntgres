package reference_test

import (
	"context"
	"fmt"
	"testing"
)

func TestReferenceSequencePreparedQueued(t *testing.T) {
	for _, action := range []string{"rename-commit", "rename-rollback", "drop-commit"} {
		t.Run("Queued/"+action, func(t *testing.T) {
			p := newReferenceScenario(t)
			if p.postgres == nil {
				return
			} // O: real lock queue and captured OID.
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s START 10")
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 10)
			var oid int64
			if err := a.QueryRowContext(p.ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			held, err := b.PrepareContext(p.ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			ap, bp := sequencePreparedPID(t, p, a), sequencePreparedPID(t, p, b)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := "ALTER SEQUENCE s RENAME TO identity"
			if action == "drop-commit" {
				query = "DROP SEQUENCE s"
			}
			referenceLockExec(t, p.ctx, tx, query, 0)
			await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
				rows, err := held.QueryContext(context.WithValue(ctx, referenceRecorderKey{}, p.postgresRecord))
				return relationOutcome{rows: rows, err: err}
			})
			referenceSequenceWaitBehind(t, p.ctx, bp, ap, oid)
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
				if outcome.rows != nil {
					outcome.rows.Close()
					t.Fatal("failed original-OID opening returned cursor")
				}
				want := controlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
				referenceAssertDiagnostic(t, outcome.err, &want)
			} else {
				if outcome.err != nil {
					t.Fatal(outcome.err)
				}
				referenceSequenceRows(t, outcome.rows, "nextval", 11)
				sequencePreparedRecorded(t, referenceBackend{recorder: p.postgresRecord, pg: true}, referenceStep{query: "SELECT nextval('s')", columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(11)}}})
			}
			if action != "rename-rollback" {
				var v int64
				err := held.QueryRowContext(p.ctx).Scan(&v)
				want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
				want.Position = "16"
				referenceAssertDiagnostic(t, err, &want)
				if action == "rename-commit" {
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('identity')", 12)
				}
			} else {
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 12)
			}
		})
	}
}
