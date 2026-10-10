package reference_test

import (
	"context"
	"fmt"
	"testing"
)

func TestReferencePreparedTransactionSSIPreparedPivot(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		incoming, pivot := referenceLockConnections(t, p.ctx, b.db)
		outgoing := sequenceCoreConn(t, p.ctx, b.db)
		for _, name := range []string{"prepared_x", "prepared_y"} {
			referenceLockExec(t, p.ctx, outgoing, "CREATE TABLE "+name+"(v INT)", 0)
			referenceLockExec(t, p.ctx, outgoing, "INSERT INTO "+name+" VALUES(0)", 1)
		}
		for _, x := range []referenceExecutor{incoming, pivot, outgoing} {
			referenceLockExec(t, p.ctx, x, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
		}
		preparedReferenceRows(t, p, b, incoming, "SELECT v FROM prepared_x", 0)
		preparedReferenceRows(t, p, b, pivot, "SELECT v FROM prepared_y", 0)
		referenceLockExec(t, p.ctx, pivot, "UPDATE prepared_x SET v=1", 1)
		preparedReferencePrepare(t, p, b, pivot, "ssi_pivot")
		b.db.SetMaxIdleConns(0)
		pivot.Close()
		referenceLockExec(t, p.ctx, outgoing, "UPDATE prepared_y SET v=2", 1)
		_, err := outgoing.ExecContext(p.ctx, "COMMIT")
		want := serializableReferenceWant("Canceled on commit attempt with conflict in from prepared pivot.")
		referenceAssertDiagnostic(t, err, &want)
		referenceLockExec(t, p.ctx, outgoing, "ROLLBACK", 0)
		preparedReferenceOne(t, p, outgoing, "ssi_pivot")
		referenceLockExec(t, p.ctx, incoming, "ROLLBACK", 0)
		referenceLockExec(t, p.ctx, outgoing, "COMMIT PREPARED 'ssi_pivot'", 0)
		preparedReferenceRows(t, p, b, outgoing, "SELECT v FROM prepared_x", 1)
		preparedReferenceRows(t, p, b, outgoing, "SELECT v FROM prepared_y", 0)
	})
}

func TestReferencePreparedTransactionSSIImmuneWriter(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				incoming, reader := referenceLockConnections(t, p.ctx, b.db)
				writer := sequenceCoreConn(t, p.ctx, b.db)
				for _, name := range []string{"prepared_read_x", "prepared_read_y"} {
					referenceLockExec(t, p.ctx, writer, "CREATE TABLE "+name+"(v INT)", 0)
					referenceLockExec(t, p.ctx, writer, "INSERT INTO "+name+" VALUES(0)", 1)
				}
				for _, x := range []referenceExecutor{incoming, reader, writer} {
					referenceLockExec(t, p.ctx, x, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
				}
				preparedReferenceRows(t, p, b, incoming, "SELECT v FROM prepared_read_y", 0)
				referenceLockExec(t, p.ctx, reader, "UPDATE prepared_read_y SET v=2", 1)
				referenceLockExec(t, p.ctx, writer, "UPDATE prepared_read_x SET v=1", 1)
				if prepared {
					preparedReferencePrepare(t, p, b, writer, "immune_writer")
					entry := preparedReferenceOne(t, p, writer, "immune_writer")
					rows, err := reader.QueryContext(p.ctx, "SELECT v FROM prepared_read_x")
					if rows != nil {
						rows.Close()
						t.Fatal("failed prepared conflict returned rows")
					}
					want := serializableReferenceWant(fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", entry.xid))
					referenceAssertDiagnostic(t, err, &want)
					referenceLockExec(t, p.ctx, reader, "ROLLBACK", 0)
					referenceLockExec(t, p.ctx, incoming, "ROLLBACK", 0)
					referenceLockExec(t, p.ctx, writer, "COMMIT PREPARED 'immune_writer'", 0)
					preparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_x", 1)
				} else {
					preparedReferenceRows(t, p, b, reader, "SELECT v FROM prepared_read_x", 0)
					// incoming -> reader -> writer: the terminal writer finalizes first
					// and dooms the still-active reader pivot, not itself (REL_18_0
					// PreCommit_CheckForSerializationFailure). The prepared writer can
					// subsequently roll back without publishing either transaction's data.
					preparedReferencePrepare(t, p, b, writer, "immune_writer")
					preparedReferenceOne(t, p, writer, "immune_writer")
					preparedReferenceCleanup(t, b, "failed_ssi")
					_, err := reader.ExecContext(p.ctx, "PREPARE TRANSACTION 'failed_ssi'")
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					referenceAssertDiagnostic(t, err, &want)
					preparedReferenceAbsent(t, p, writer, "failed_ssi")
					referenceLockExec(t, p.ctx, incoming, "ROLLBACK", 0)
					referenceLockExec(t, p.ctx, writer, "ROLLBACK PREPARED 'immune_writer'", 0)
					preparedReferenceAbsent(t, p, writer, "immune_writer")
					preparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_x", 0)
					preparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_y", 0)
					referenceLockExec(t, p.ctx, reader, "BEGIN", 0)
					referenceLockExec(t, p.ctx, reader, "ROLLBACK", 0)
				}
			})
		})
	}
}

func TestReferencePreparedTransactionSSIDeferrable(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				writer, reader := referenceLockConnections(t, p.ctx, b.db)
				finish := sequenceCoreConn(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, finish, "CREATE TABLE prepared_safe(v INT)", 0)
				referenceLockExec(t, p.ctx, finish, "INSERT INTO prepared_safe VALUES(0)", 1)
				referenceLockExec(t, p.ctx, writer, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
				preparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_safe", 0)
				referenceLockExec(t, p.ctx, writer, "UPDATE prepared_safe SET v=1", 1)
				// Only PostgreSQL exposes public SafeSnapshot admission. Native's
				// exact admitted predecessor is covered by the real private candidate
				// barrier, not a scheduling-dependent worker-start assumption.
				var await func() relationOutcome
				pid := 0
				if b.pg {
					pid = referenceLockPID(t, p.ctx, reader)
					referenceLockExec(t, p.ctx, reader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
					await = referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						return referenceRowQuery(ctx, reader, "SELECT v FROM prepared_safe", false)
					})
					serializableReferenceSafeWait(t, p.ctx, pid)
				}
				preparedReferencePrepare(t, p, b, writer, "safe_writer")
				b.db.SetMaxIdleConns(0)
				writer.Close()
				if b.pg {
					serializableReferenceSafeWait(t, p.ctx, pid)
				}
				// Both backends synchronously prove PREPARE and physical origin
				// exit did not declare this writer committed/safe. lock_timeout
				// does not cover the safe-snapshot wait; statement_timeout does.
				timedReader := sequenceCoreConn(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, timedReader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
				referenceLockExec(t, p.ctx, timedReader, "SET LOCAL statement_timeout='200ms'", 0)
				referenceLockExec(t, p.ctx, timedReader, "SET LOCAL lock_timeout='1ms'", 0)
				rows, err := timedReader.QueryContext(p.ctx, "SELECT v FROM prepared_safe")
				if rows != nil {
					rows.Close()
					t.Fatal("unresolved prepared candidate returned rows")
				}
				want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				referenceAssertDiagnostic(t, err, &want)
				referenceLockExec(t, p.ctx, timedReader, "ROLLBACK", 0)
				referenceLockExec(t, p.ctx, finish, completion+" PREPARED 'safe_writer'", 0)
				if b.pg {
					// Genuine observed admission retains predecessor0, even on commit.
					referenceRowValues(t, await(), 0)
					referenceLockExec(t, p.ctx, reader, "COMMIT", 0)
				}
				v := int64(0)
				if completion == "COMMIT" {
					v = 1
				}
				preparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_safe", v)
				// A fresh candidate after actual completion is now immediately safe.
				referenceLockExec(t, p.ctx, timedReader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
				preparedReferenceRows(t, p, b, timedReader, "SELECT v FROM prepared_safe", v)
				referenceLockExec(t, p.ctx, timedReader, "COMMIT", 0)
			})
		})
	}
}
