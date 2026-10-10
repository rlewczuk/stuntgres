package reference_test

import (
	"strings"
	"testing"
)

func TestReferencePreparedTransactionSettings(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		show := func(x referenceExecutor, want string) {
			rows, err := x.QueryContext(p.ctx, "SHOW application_name")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got string
			if !rows.Next() {
				t.Fatal("missing setting", rows.Err())
			}
			if err := rows.Scan(&got); err != nil || got != want {
				t.Fatal(got, want, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra setting", rows.Err())
			}
		}
		referenceLockExec(t, p.ctx, peer, "SET application_name='finisher'", 0)
		for _, c := range []struct {
			sql, want string
			local     bool
		}{
			{"SET application_name='candidate'", "candidate", false},
			{"SET LOCAL application_name='local'", "baseline", false},
			{"SET application_name='candidate'", "candidate", true},
		} {
			referenceLockExec(t, p.ctx, a, "SET application_name='baseline'", 0)
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			referenceLockExec(t, p.ctx, a, c.sql, 0)
			if c.local {
				referenceLockExec(t, p.ctx, a, "SET LOCAL application_name='local'", 0)
			}
			preparedReferencePrepare(t, p, b, a, "settings")
			show(a, c.want)
			show(peer, "finisher")
			referenceLockExec(t, p.ctx, peer, "COMMIT PREPARED 'settings'", 0)
			show(a, c.want)
			show(peer, "finisher")
		}
		referenceLockExec(t, p.ctx, a, "SET application_name='baseline'", 0)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "SET application_name='candidate'", 0)
		referenceLockExec(t, p.ctx, a, "SAVEPOINT child", 0)
		gid := strings.Repeat("x", 200)
		preparedReferenceCleanup(t, b, gid)
		preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
		show(a, "baseline")
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
	})
}

func TestReferencePreparedTransactionTemporaryFootprint(t *testing.T) {
	for _, access := range []string{
		"CREATE TEMP SEQUENCE new_temp", "SELECT nextval('pg_temp.old_temp')", "SELECT currval('pg_temp.old_temp')",
		"SELECT setval('pg_temp.old_temp',9)", "SELECT lastval()", "SELECT last_value FROM pg_temp.old_temp",
		"ALTER SEQUENCE pg_temp.old_temp RESTART WITH 9", "DROP SEQUENCE pg_temp.old_temp",
	} {
		t.Run(access, func(t *testing.T) {
			preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, peer := referenceLockConnections(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_temp(v INT)", 0)
				referenceLockExec(t, p.ctx, a, "CREATE TEMP SEQUENCE old_temp", 0)
				sequenceProbeValue(t, p.ctx, a, "SELECT nextval('pg_temp.old_temp')", 1)
				// Merely owning an old temporary object is not a blanket prohibition.
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				preparedReferencePrepare(t, p, b, a, "unused_temp")
				referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED 'unused_temp'", 0)
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_temp VALUES(1)", 1)
				referenceLockExec(t, p.ctx, a, "SAVEPOINT child", 0)
				if strings.HasPrefix(access, "SELECT") {
					rows, err := a.QueryContext(p.ctx, access)
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
				} else {
					referenceLockExec(t, p.ctx, a, access, 0)
				}
				referenceLockExec(t, p.ctx, a, "ROLLBACK TO child", 0)
				preparedReferenceCleanup(t, b, "temporary")
				preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION 'temporary'", "0A000", "cannot PREPARE a transaction that has operated on temporary objects")
				preparedReferenceAbsent(t, p, peer, "temporary")
				preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_temp")
				referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_temp VALUES(2)", 1)
				preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_temp", 2)
			})
		})
	}
}

func TestReferencePreparedTransactionPersistentSequenceDraws(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE prepared_draw", 0)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('prepared_draw')", 1)
		preparedReferencePrepare(t, p, b, a, "draw")
		referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED 'draw'", 0)
		sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('prepared_draw')", 2)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		sequenceProbeValue(t, p.ctx, a, "SELECT nextval('prepared_draw')", 3)
		gid := strings.Repeat("x", 200)
		preparedReferenceCleanup(t, b, gid)
		preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
		sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('prepared_draw')", 4)
	})
}

func TestReferencePreparedTransactionSyntax(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		preparedReferenceCleanup(t, b, "syntax")
		for _, c := range []struct{ q, token, position string }{
			{"PREPARE TRANSACTION $1", "$1", "21"}, {"PREPARE TRANSACTION gid", "gid", "21"},
			{"COMMIT PREPARED 'syntax' AND CHAIN", "AND", "26"}, {"ROLLBACK PREPARED 'syntax' extra", "extra", "28"},
		} {
			want := controlsReferenceDiagnostic("42601", "syntax error at or near \""+c.token+"\"")
			want.Position = c.position
			_, err := a.ExecContext(p.ctx, c.q)
			referenceAssertDiagnostic(t, err, &want)
			stmt, err := a.PrepareContext(p.ctx, c.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid Prepare returned statement")
			}
			referenceAssertDiagnostic(t, err, &want)
			preparedReferenceAbsent(t, p, peer, "syntax")
		}
	})
}

func TestReferencePreparedTransactionUnsupportedSessionBoundaries(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		// TEMP now executes on both backends and proves the real PREPARE restriction.
		// Other excluded features keep their independent native syntax/function errors.
		for _, c := range []struct {
			q, message string
			listening  bool
		}{
			{"CREATE TEMP TABLE prepared_boundary(v INT)", "cannot PREPARE a transaction that has operated on temporary objects", false},
			{"DECLARE prepared_cursor CURSOR WITH HOLD FOR SELECT 1", "cannot PREPARE a transaction that has created a cursor WITH HOLD", false},
			{"LISTEN prepared_channel", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY", false},
			// Async_UnlistenAll returns without queuing actions in a clean session.
			// Preserve that successful PG branch as well as a real UNLISTEN witness.
			{"UNLISTEN *", "", false},
			{"UNLISTEN *", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY", true},
			{"NOTIFY prepared_channel", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY", false},
			{"SELECT pg_export_snapshot()", "cannot PREPARE a transaction that has exported snapshots", false},
		} {
			if b.pg && c.listening {
				referenceLockExec(t, p.ctx, a, "LISTEN prepared_channel", 0)
			}
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			rows, err := a.QueryContext(p.ctx, c.q)
			if !b.pg && !strings.HasPrefix(c.q, "CREATE TEMP TABLE") {
				if rows != nil {
					rows.Close()
					t.Fatal("unsupported boundary returned rows")
				}
				want := controlsReferenceDiagnostic("0A000", "statement is unsupported")
				want.Position = "1"
				switch {
				case strings.HasPrefix(c.q, "DECLARE"):
					want = controlsReferenceDiagnostic("42601", `syntax error at or near "DECLARE"`)
					want.Position = "1"
				case strings.HasPrefix(c.q, "SELECT"):
					want = controlsReferenceDiagnostic("0A000", "functions are unsupported")
					want.Position = "8"
				}
				referenceAssertDiagnostic(t, err, &want)
				referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
				continue
			}
			if err != nil {
				t.Fatal(c.q, err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			preparedReferenceCleanup(t, b, "boundary")
			if c.message == "" {
				referenceLockExec(t, p.ctx, a, "PREPARE TRANSACTION 'boundary'", 0)
				preparedReferenceOne(t, p, peer, "boundary")
				referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED 'boundary'", 0)
			} else {
				preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION 'boundary'", "0A000", c.message)
			}
			preparedReferenceAbsent(t, p, peer, "boundary")
			if c.listening {
				referenceLockExec(t, p.ctx, a, "UNLISTEN *", 0)
			}
		}
	})
}
