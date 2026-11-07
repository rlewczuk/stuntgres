package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferencePGNotifyDelivery proves the NOTIFY-equivalent message for the
// function form: same channel/payload/PID fields, statement-end staging,
// commit-only publication, rollback discard, and same-transaction folding.
func TestReferencePGNotifyDelivery(t *testing.T) {
	t.Run("direct-delivery", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN pg_notify_chan")
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('pg_notify_chan','direct')")
			pgNotifyReceive(t, p, b, r, "pg_notify_chan", "direct", pid)
			pgNotifyAbsent(t, p, b, r)
		})
	})

	t.Run("coder-shape", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, `LISTEN "Case Sensitive"`)
			if _, err := s.ExecContext(p.Ctx, "select pg_notify('Case Sensitive', $1)", "coder-payload"); err != nil {
				t.Fatal(err)
			}
			pgNotifyReceive(t, p, b, r, "Case Sensitive", "coder-payload", pid)
			if !b.PG {
				// Native normalizeArguments/importParameter must accept []byte
				// values, including a typed nil that is the empty payload.
				for _, arg := range []struct {
					value   any
					payload string
				}{{[]byte("coder-payload"), "coder-payload"}, {[]byte(nil), ""}} {
					if _, err := s.ExecContext(p.Ctx, "select pg_notify('Case Sensitive', $1)", arg.value); err != nil {
						t.Fatal(err)
					}
					pgNotifyReceive(t, p, b, r, "Case Sensitive", arg.payload, pid)
				}
			}
		})
	})

	t.Run("prepared-parameters", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN prepared")
			stmt, err := s.PrepareContext(p.Ctx, "SELECT pg_notify($1, $2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			// Parse/Describe must not execute the function. The two-parameter
			// contract is pinned independently by the binding unit tests;
			// database/sql.Stmt exposes no parameter-count metadata.
			pgNotifyAbsent(t, p, b, r)
			if _, err := stmt.ExecContext(p.Ctx, "prepared", "p1"); err != nil {
				t.Fatal(err)
			}
			pgNotifyReceive(t, p, b, r, "prepared", "p1", pid)
			pgNotifyAbsent(t, p, b, r)
			if !b.PG {
				if _, err := stmt.ExecContext(p.Ctx, "prepared", []byte("p1")); err != nil {
					t.Fatal(err)
				}
				pgNotifyReceive(t, p, b, r, "prepared", "p1", pid)
				pgNotifyAbsent(t, p, b, r)
			}
		})
	})

	t.Run("prepared-literal", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN prepared_literal")
			stmt, err := s.PrepareContext(p.Ctx, "SELECT pg_notify('prepared_literal','p2')")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			pgNotifyAbsent(t, p, b, r)
			if _, err := stmt.ExecContext(p.Ctx); err != nil {
				t.Fatal(err)
			}
			pgNotifyReceive(t, p, b, r, "prepared_literal", "p2", pid)
		})
	})

	t.Run("commit-only", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN tx")
			pgNotifySQL(t, p, b, s, "BEGIN")
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('tx','committed')")
			pgNotifyAbsent(t, p, b, r)
			pgNotifySQL(t, p, b, s, "COMMIT")
			pgNotifyReceive(t, p, b, r, "tx", "committed", pid)
		})
	})

	t.Run("rollback-discards", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN tx")
			pgNotifySQL(t, p, b, s, "BEGIN")
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('tx','rolled-back')")
			pgNotifySQL(t, p, b, s, "ROLLBACK")
			pgNotifyAbsent(t, p, b, r)
			// A later committed event proves the queue carries no stale state.
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('tx','sentinel')")
			pgNotifyReceive(t, p, b, r, "tx", "sentinel", pid)
		})
	})

	t.Run("read-only-commits", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN tx")
			pgNotifySQL(t, p, b, s, "BEGIN READ ONLY")
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('tx','read-only')")
			pgNotifySQL(t, p, b, s, "COMMIT")
			pgNotifyReceive(t, p, b, r, "tx", "read-only", pid)
		})
	})

	t.Run("order-and-dedup", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			for _, channel := range []string{"a", "b", "dup"} {
				pgNotifySQL(t, p, b, r, "LISTEN "+channel)
			}
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT pg_notify('a','1'), pg_notify('b','2')",
				[]string{"pg_notify", "pg_notify"}, []string{"VOID", "VOID"}, [][]any{{"", ""}},
			))
			pgNotifyReceive(t, p, b, r, "a", "1", pid)
			pgNotifyReceive(t, p, b, r, "b", "2", pid)
			// Identical channel and payload in one transaction fold.
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT pg_notify('dup','same'), pg_notify('dup','same')",
				[]string{"pg_notify", "pg_notify"}, []string{"VOID", "VOID"}, [][]any{{"", ""}},
			))
			pgNotifyReceive(t, p, b, r, "dup", "same", pid)
			pgNotifyAbsent(t, p, b, r)
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('dup','sentinel')")
			pgNotifyReceive(t, p, b, r, "dup", "sentinel", pid)
		})
	})

	t.Run("error-atomicity", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN tx")
			want := fixture.ControlsReferenceDiagnostic("22023", "channel name cannot be empty")
			failure := fixture.ErrorDiagnostic("SELECT pg_notify('tx','queued'), pg_notify('','bad')", want)
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, s, b.Recorder, failure)
			// The first call's staged event is dropped with the failed statement.
			pgNotifyAbsent(t, p, b, r)
			pgNotifyVoid(t, p, b, s, "SELECT pg_notify('tx','after-error')")
			pgNotifyReceive(t, p, b, r, "tx", "after-error", pid)
		})
	})

	t.Run("null-payload", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN empty")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT pg_notify('empty', NULL)", []string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
			))
			pgNotifyReceive(t, p, b, r, "empty", "", pid)
		})
	})
}
