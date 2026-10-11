package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedTransactionSSIPreparedPivot(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		incoming, pivot := fixture.LockConnections(t, p.Ctx, b.DB)
		outgoing := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		for _, name := range []string{"prepared_x", "prepared_y"} {
			fixture.LockExec(t, p.Ctx, outgoing, "CREATE TABLE "+name+"(v INT)", 0)
			fixture.LockExec(t, p.Ctx, outgoing, "INSERT INTO "+name+" VALUES(0)", 1)
		}
		for _, x := range []fixture.Executor{incoming, pivot, outgoing} {
			fixture.LockExec(t, p.Ctx, x, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
		}
		fixture.PreparedReferenceRows(t, p, b, incoming, "SELECT v FROM prepared_x", 0)
		fixture.PreparedReferenceRows(t, p, b, pivot, "SELECT v FROM prepared_y", 0)
		fixture.LockExec(t, p.Ctx, pivot, "UPDATE prepared_x SET v=1", 1)
		fixture.PreparedReferencePrepare(t, p, b, pivot, "ssi_pivot")
		b.DB.SetMaxIdleConns(0)
		pivot.Close()
		fixture.LockExec(t, p.Ctx, outgoing, "UPDATE prepared_y SET v=2", 1)
		_, err := outgoing.ExecContext(p.Ctx, "COMMIT")
		want := serializableReferenceWant("Canceled on commit attempt with conflict in from prepared pivot.")
		fixture.AssertDiagnostic(t, err, &want)
		fixture.LockExec(t, p.Ctx, outgoing, "ROLLBACK", 0)
		fixture.PreparedReferenceOne(t, p, outgoing, "ssi_pivot")
		fixture.LockExec(t, p.Ctx, incoming, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, outgoing, "COMMIT PREPARED 'ssi_pivot'", 0)
		fixture.PreparedReferenceRows(t, p, b, outgoing, "SELECT v FROM prepared_x", 1)
		fixture.PreparedReferenceRows(t, p, b, outgoing, "SELECT v FROM prepared_y", 0)
	})
}

func TestReferencePreparedTransactionSSIImmuneWriter(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				incoming, reader := fixture.LockConnections(t, p.Ctx, b.DB)
				writer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				for _, name := range []string{"prepared_read_x", "prepared_read_y"} {
					fixture.LockExec(t, p.Ctx, writer, "CREATE TABLE "+name+"(v INT)", 0)
					fixture.LockExec(t, p.Ctx, writer, "INSERT INTO "+name+" VALUES(0)", 1)
				}
				for _, x := range []fixture.Executor{incoming, reader, writer} {
					fixture.LockExec(t, p.Ctx, x, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
				}
				fixture.PreparedReferenceRows(t, p, b, incoming, "SELECT v FROM prepared_read_y", 0)
				fixture.LockExec(t, p.Ctx, reader, "UPDATE prepared_read_y SET v=2", 1)
				fixture.LockExec(t, p.Ctx, writer, "UPDATE prepared_read_x SET v=1", 1)
				if prepared {
					fixture.PreparedReferencePrepare(t, p, b, writer, "immune_writer")
					entry := fixture.PreparedReferenceOne(t, p, writer, "immune_writer")
					rows, err := reader.QueryContext(p.Ctx, "SELECT v FROM prepared_read_x")
					if rows != nil {
						rows.Close()
						t.Fatal("failed prepared conflict returned rows")
					}
					want := serializableReferenceWant(fmt.Sprintf("Canceled on conflict out to pivot %d, during read.", entry.Xid))
					fixture.AssertDiagnostic(t, err, &want)
					fixture.LockExec(t, p.Ctx, reader, "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, incoming, "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, writer, "COMMIT PREPARED 'immune_writer'", 0)
					fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_x", 1)
				} else {
					fixture.PreparedReferenceRows(t, p, b, reader, "SELECT v FROM prepared_read_x", 0)
					// incoming -> reader -> writer: the terminal writer finalizes first
					// and dooms the still-active reader pivot, not itself (REL_18_0
					// PreCommit_CheckForSerializationFailure). The prepared writer can
					// subsequently roll back without publishing either transaction's data.
					fixture.PreparedReferencePrepare(t, p, b, writer, "immune_writer")
					fixture.PreparedReferenceOne(t, p, writer, "immune_writer")
					fixture.PreparedReferenceCleanup(t, b, "failed_ssi")
					_, err := reader.ExecContext(p.Ctx, "PREPARE TRANSACTION 'failed_ssi'")
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					fixture.AssertDiagnostic(t, err, &want)
					fixture.PreparedReferenceAbsent(t, p, writer, "failed_ssi")
					fixture.LockExec(t, p.Ctx, incoming, "ROLLBACK", 0)
					fixture.LockExec(t, p.Ctx, writer, "ROLLBACK PREPARED 'immune_writer'", 0)
					fixture.PreparedReferenceAbsent(t, p, writer, "immune_writer")
					fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_x", 0)
					fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_read_y", 0)
					fixture.LockExec(t, p.Ctx, reader, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, reader, "ROLLBACK", 0)
				}
			})
		})
	}
}

func TestReferencePreparedTransactionSSIDeferrable(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				writer, reader := fixture.LockConnections(t, p.Ctx, b.DB)
				finish := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, finish, "CREATE TABLE prepared_safe(v INT)", 0)
				fixture.LockExec(t, p.Ctx, finish, "INSERT INTO prepared_safe VALUES(0)", 1)
				fixture.LockExec(t, p.Ctx, writer, "BEGIN ISOLATION LEVEL SERIALIZABLE", 0)
				fixture.PreparedReferenceRows(t, p, b, writer, "SELECT v FROM prepared_safe", 0)
				fixture.LockExec(t, p.Ctx, writer, "UPDATE prepared_safe SET v=1", 1)
				// Only PostgreSQL exposes public SafeSnapshot admission. Native's
				// exact admitted predecessor is covered by the real private candidate
				// barrier, not a scheduling-dependent worker-start assumption.
				var await func() fixture.RelationOutcome
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, reader)
					fixture.LockExec(t, p.Ctx, reader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
					await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowQuery(ctx, reader, "SELECT v FROM prepared_safe", false)
					})
					serializableReferenceSafeWait(t, p.Ctx, pid)
				}
				fixture.PreparedReferencePrepare(t, p, b, writer, "safe_writer")
				b.DB.SetMaxIdleConns(0)
				writer.Close()
				if b.PG {
					serializableReferenceSafeWait(t, p.Ctx, pid)
				}
				// Both backends synchronously prove PREPARE and physical origin
				// exit did not declare this writer committed/safe. lock_timeout
				// does not cover the safe-snapshot wait; statement_timeout does.
				timedReader := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, timedReader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
				fixture.LockExec(t, p.Ctx, timedReader, "SET LOCAL statement_timeout='200ms'", 0)
				fixture.LockExec(t, p.Ctx, timedReader, "SET LOCAL lock_timeout='1ms'", 0)
				rows, err := timedReader.QueryContext(p.Ctx, "SELECT v FROM prepared_safe")
				if rows != nil {
					rows.Close()
					t.Fatal("unresolved prepared candidate returned rows")
				}
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				fixture.AssertDiagnostic(t, err, &want)
				fixture.LockExec(t, p.Ctx, timedReader, "ROLLBACK", 0)
				fixture.LockExec(t, p.Ctx, finish, completion+" PREPARED 'safe_writer'", 0)
				if b.PG {
					// Genuine observed admission retains predecessor0, even on commit.
					fixture.RowValues(t, await(), 0)
					fixture.LockExec(t, p.Ctx, reader, "COMMIT", 0)
				}
				v := int64(0)
				if completion == "COMMIT" {
					v = 1
				}
				fixture.PreparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_safe", v)
				// A fresh candidate after actual completion is now immediately safe.
				fixture.LockExec(t, p.Ctx, timedReader, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0)
				fixture.PreparedReferenceRows(t, p, b, timedReader, "SELECT v FROM prepared_safe", v)
				fixture.LockExec(t, p.Ctx, timedReader, "COMMIT", 0)
			})
		})
	}
}
