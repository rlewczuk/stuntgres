package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferencePGNotifyDiagnostics pins the statement's channel/payload
// validation (22023), the missing-signature diagnostics (42883), and the exact
// accepted byte boundaries. PostgreSQL validates the function body at
// execution, so 22023 appears at execute even for a prepared statement; name
// resolution fails during analysis, so 42883 appears at prepare.
func TestReferencePGNotifyDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []struct {
		name, query, code, message string
	}{
		{"empty-channel", "SELECT pg_notify('', 'p')", "22023", "channel name cannot be empty"},
		{"null-channel", "SELECT pg_notify(NULL, 'p')", "22023", "channel name cannot be empty"},
		{"both-null", "SELECT pg_notify(NULL, NULL)", "22023", "channel name cannot be empty"},
		{"channel-64", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("a", 64)), "22023", "channel name too long"},
		{"channel-66-cjk", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("雪", 22)), "22023", "channel name too long"},
		{"channel-64-latin", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("é", 32)), "22023", "channel name too long"},
		{"payload-8000", fmt.Sprintf("SELECT pg_notify('chan','%s')", strings.Repeat("a", 8000)), "22023", "payload string too long"},
		{"payload-8001-cjk", fmt.Sprintf("SELECT pg_notify('chan','%s')", strings.Repeat("雪", 2667)), "22023", "payload string too long"},
		{"payload-8000-latin", fmt.Sprintf("SELECT pg_notify('chan','%s')", strings.Repeat("é", 4000)), "22023", "payload string too long"},
		{"integer-argument", "SELECT pg_notify('chan', 1)", "42883", "function pg_notify(unknown, integer) does not exist"},
		{"numeric-argument", "SELECT pg_notify('chan', 1.0)", "42883", "function pg_notify(unknown, numeric) does not exist"},
		{"named-forward", "SELECT pg_notify(channel => 'chan', payload => 'p')", "42883", "function pg_notify(channel => unknown, payload => unknown) does not exist"},
		{"named-reverse", "SELECT pg_notify(payload => 'p', channel => 'chan')", "42883", "function pg_notify(payload => unknown, channel => unknown) does not exist"},
		{"no-arguments", "SELECT pg_notify()", "42883", "function pg_notify() does not exist"},
		{"one-argument", "SELECT pg_notify('chan')", "42883", "function pg_notify(unknown) does not exist"},
		{"three-arguments", "SELECT pg_notify('chan','p','x')", "42883", "function pg_notify(unknown, unknown, unknown) does not exist"},
		{"quoted-name", `SELECT "PG_NOTIFY"('chan','p')`, "42883", "function PG_NOTIFY(unknown, unknown) does not exist"},
	}
	pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		s, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				name := c.name + "/direct"
				if prepared {
					name = c.name + "/prepared"
				}
				t.Run(name, func(t *testing.T) {
					step := fixture.ErrorDiagnostic(c.query, fixture.ControlsReferenceDiagnostic(c.code, c.message))
					if c.code == "42883" {
						step = fixture.ImplicitError(c.query, fixture.Diagnostic{
							Code: c.code, Message: c.message, Hint: hint, Position: fixture.Position(8),
						})
					}
					step.SelectRows = true
					step.ErrorStage = "execute"
					if prepared {
						if c.code == "42883" {
							step.ErrorStage = "prepare"
						}
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, s, b.Recorder, step)
					// Every autocommit failure leaves the session usable.
					pgNotifyHealth(t, p, b, s)
				})
			}
		}

		// Byte, not rune, lengths: each accepted boundary is a VOID row.
		accepted := []struct{ name, query string }{
			{"channel-63", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("a", 63))},
			{"channel-63-cjk", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("雪", 21))},
			{"channel-63-latin", fmt.Sprintf("SELECT pg_notify('%s','p')", strings.Repeat("é", 31)+"a")},
			{"payload-7999", fmt.Sprintf("SELECT pg_notify('c','%s')", strings.Repeat("a", 7999))},
			{"payload-7998-cjk", fmt.Sprintf("SELECT pg_notify('c','%s')", strings.Repeat("雪", 2666))},
		}
		for _, c := range accepted {
			t.Run(c.name, func(t *testing.T) {
				step := fixture.Select(c.query, []string{"pg_notify"}, []string{"VOID"}, [][]any{{""}})
				fixture.RunStep(t, p.Ctx, s, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Prepared(step))
			})
		}
	})

	t.Run("aborted-transaction-precedence", func(t *testing.T) {
		pgNotifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := pgNotifyConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pid := uint64(pgNotifyPID(t, p, s))
			pgNotifySQL(t, p, b, r, "LISTEN chan")
			pgNotifySQL(t, p, b, s, "BEGIN")
			empty := fixture.ControlsReferenceDiagnostic("22023", "channel name cannot be empty")
			failure := fixture.ErrorDiagnostic("SELECT pg_notify('','p')", empty)
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, s, b.Recorder, failure)
			aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			step := fixture.ErrorDiagnostic("SELECT pg_notify('chan','p')", aborted)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, s, b.Recorder, step)
			pgNotifySQL(t, p, b, s, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT pg_notify('chan','p')", []string{"pg_notify"}, []string{"VOID"}, [][]any{{""}},
			))
			pgNotifyReceive(t, p, b, r, "chan", "p", pid)
		})
	})
}
