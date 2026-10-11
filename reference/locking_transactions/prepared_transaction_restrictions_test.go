package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedTransactionSettings(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		show := func(x fixture.Executor, want string) {
			rows, err := x.QueryContext(p.Ctx, "SHOW application_name")
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
		fixture.LockExec(t, p.Ctx, peer, "SET application_name='finisher'", 0)
		for _, c := range []struct {
			sql, want string
			local     bool
		}{
			{"SET application_name='candidate'", "candidate", false},
			{"SET LOCAL application_name='local'", "baseline", false},
			{"SET application_name='candidate'", "candidate", true},
		} {
			fixture.LockExec(t, p.Ctx, a, "SET application_name='baseline'", 0)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, c.sql, 0)
			if c.local {
				fixture.LockExec(t, p.Ctx, a, "SET LOCAL application_name='local'", 0)
			}
			fixture.PreparedReferencePrepare(t, p, b, a, "settings")
			show(a, c.want)
			show(peer, "finisher")
			fixture.LockExec(t, p.Ctx, peer, "COMMIT PREPARED 'settings'", 0)
			show(a, c.want)
			show(peer, "finisher")
		}
		fixture.LockExec(t, p.Ctx, a, "SET application_name='baseline'", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "SET application_name='candidate'", 0)
		fixture.LockExec(t, p.Ctx, a, "SAVEPOINT child", 0)
		gid := strings.Repeat("x", 200)
		fixture.PreparedReferenceCleanup(t, b, gid)
		fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
		show(a, "baseline")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}

func TestReferencePreparedTransactionTemporaryFootprint(t *testing.T) {
	for _, access := range []string{
		"CREATE TEMP SEQUENCE new_temp", "SELECT nextval('pg_temp.old_temp')", "SELECT currval('pg_temp.old_temp')",
		"SELECT setval('pg_temp.old_temp',9)", "SELECT lastval()", "SELECT last_value FROM pg_temp.old_temp",
		"ALTER SEQUENCE pg_temp.old_temp RESTART WITH 9", "DROP SEQUENCE pg_temp.old_temp",
	} {
		t.Run(access, func(t *testing.T) {
			fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_temp(v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE TEMP SEQUENCE old_temp", 0)
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('pg_temp.old_temp')", 1)
				// Merely owning an old temporary object is not a blanket prohibition.
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.PreparedReferencePrepare(t, p, b, a, "unused_temp")
				fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'unused_temp'", 0)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_temp VALUES(1)", 1)
				fixture.LockExec(t, p.Ctx, a, "SAVEPOINT child", 0)
				if strings.HasPrefix(access, "SELECT") {
					rows, err := a.QueryContext(p.Ctx, access)
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
					fixture.LockExec(t, p.Ctx, a, access, 0)
				}
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK TO child", 0)
				fixture.PreparedReferenceCleanup(t, b, "temporary")
				fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'temporary'", "0A000", "cannot PREPARE a transaction that has operated on temporary objects")
				fixture.PreparedReferenceAbsent(t, p, peer, "temporary")
				fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_temp")
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_temp VALUES(2)", 1)
				fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_temp", 2)
			})
		})
	}
}

func TestReferencePreparedTransactionPersistentSequenceDraws(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE prepared_draw", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('prepared_draw')", 1)
		fixture.PreparedReferencePrepare(t, p, b, a, "draw")
		fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'draw'", 0)
		fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('prepared_draw')", 2)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('prepared_draw')", 3)
		gid := strings.Repeat("x", 200)
		fixture.PreparedReferenceCleanup(t, b, gid)
		fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
		fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('prepared_draw')", 4)
	})
}

func TestReferencePreparedTransactionSyntax(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.PreparedReferenceCleanup(t, b, "syntax")
		for _, c := range []struct{ q, token, position string }{
			{"PREPARE TRANSACTION $1", "$1", "21"}, {"PREPARE TRANSACTION gid", "gid", "21"},
			{"COMMIT PREPARED 'syntax' AND CHAIN", "AND", "26"}, {"ROLLBACK PREPARED 'syntax' extra", "extra", "28"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \""+c.token+"\"")
			want.Position = c.position
			_, err := a.ExecContext(p.Ctx, c.q)
			fixture.AssertDiagnostic(t, err, &want)
			stmt, err := a.PrepareContext(p.Ctx, c.q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("invalid Prepare returned statement")
			}
			fixture.AssertDiagnostic(t, err, &want)
			fixture.PreparedReferenceAbsent(t, p, peer, "syntax")
		}
	})
}

func TestReferencePreparedTransactionUnsupportedSessionBoundaries(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		// TEMP now executes on both backends and proves the real PREPARE restriction.
		// WITH HOLD has an explicit native capability error; other excluded
		// features retain their independent statement/function boundaries.
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
			if b.PG && c.listening {
				fixture.LockExec(t, p.Ctx, a, "LISTEN prepared_channel", 0)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			rows, err := a.QueryContext(p.Ctx, c.q)
			if !b.PG && !strings.HasPrefix(c.q, "CREATE TEMP TABLE") {
				if rows != nil {
					rows.Close()
					t.Fatal("unsupported boundary returned rows")
				}
				want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
				want.Position = "1"
				switch {
				case strings.HasPrefix(c.q, "DECLARE"):
					want = fixture.ControlsReferenceDiagnostic("0A000", "WITH HOLD cursors are unsupported")
					want.Position = "32"
				case strings.HasPrefix(c.q, "SELECT"):
					want = fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
					want.Position = "8"
				}
				fixture.AssertDiagnostic(t, err, &want)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
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
			fixture.PreparedReferenceCleanup(t, b, "boundary")
			if c.message == "" {
				fixture.LockExec(t, p.Ctx, a, "PREPARE TRANSACTION 'boundary'", 0)
				fixture.PreparedReferenceOne(t, p, peer, "boundary")
				fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'boundary'", 0)
			} else {
				fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'boundary'", "0A000", c.message)
			}
			fixture.PreparedReferenceAbsent(t, p, peer, "boundary")
			if c.listening {
				fixture.LockExec(t, p.Ctx, a, "UNLISTEN *", 0)
			}
		}
	})
}
