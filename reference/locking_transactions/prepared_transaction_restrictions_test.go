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

func TestReferencePreparedTransactionSessionBoundaries(t *testing.T) {
	const messages = "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY"
	for _, c := range []struct {
		name, setup, action, message string
	}{
		{"temporary", "", "CREATE TEMP TABLE prepared_boundary(v INT)", "cannot PREPARE a transaction that has operated on temporary objects"},
		{"hold", "", "DECLARE prepared_cursor CURSOR WITH HOLD FOR SELECT 1", "cannot PREPARE a transaction that has created a cursor WITH HOLD"},
		{"listen", "", "LISTEN prepared_channel", messages},
		{"notify", "", "NOTIFY prepared_channel, 'discarded'", messages},
		{"pristine-unlisten-all", "", "UNLISTEN *", ""},
		{"pristine-unlisten-channel", "", "UNLISTEN prepared_channel", ""},
		{"current-subscription-only", "LISTEN prepared_channel", "", ""},
		{"duplicate-listen", "LISTEN prepared_channel", "LISTEN prepared_channel", messages},
		{"net-zero", "", "LISTEN prepared_channel; UNLISTEN prepared_channel", messages},
		{"unlisten-subscribed", "LISTEN prepared_channel", "UNLISTEN *", messages},
		{"sticky-after-all", "LISTEN prepared_channel; UNLISTEN *", "UNLISTEN prepared_channel", messages},
		{"sticky-after-discard", "LISTEN prepared_channel; DISCARD ALL", "UNLISTEN *", messages},
		{"rolled-back-first-listen", "BEGIN; LISTEN prepared_channel; ROLLBACK", "UNLISTEN *", ""},
		{"child-rollback", "", "SAVEPOINT child; LISTEN prepared_channel; NOTIFY prepared_channel; ROLLBACK TO child", ""},
		{"child-release", "", "SAVEPOINT child; NOTIFY prepared_channel; RELEASE child", messages},
		{"export", "", "SELECT pg_export_snapshot()", "cannot PREPARE a transaction that has exported snapshots"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A new fixture/pool per history case is essential: an empty
			// subscription set is NOT a pristine physical session.
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					if c.setup != "" {
						for _, q := range strings.Split(c.setup, "; ") {
							fixture.LockExec(t, p.Ctx, a, q, 0)
						}
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					if c.action != "" {
						rows, err := a.QueryContext(p.Ctx, c.action)
						if !b.PG && c.name == "export" {
							if rows != nil {
								rows.Close()
								t.Fatal("unsupported export exposed rows")
							}
							want := fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
							want.Position = "8"
							fixture.AssertDiagnostic(t, err, &want)
							fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
							return
						}
						if err != nil {
							t.Fatal(c.action, err)
						}
						for {
							for rows.Next() {
							}
							if !rows.NextResultSet() {
								break
							}
						}
						if err := rows.Err(); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
					}
					fixture.PreparedReferenceCleanup(t, b, "boundary")
					if c.message == "" {
						fixture.LockExec(t, p.Ctx, a, "PREPARE TRANSACTION 'boundary'", 0)
						fixture.PreparedReferenceOne(t, p, peer, "boundary")
						fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'boundary'", 0)
					} else {
						fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'boundary'", "0A000", c.message)
					}
					fixture.PreparedReferenceAbsent(t, p, peer, "boundary")
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(
						"SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}},
					))
				})
			}
		})
	}
	t.Run("failed-prepare-message-effects", preparedMessageBoundaryEffects)
}
