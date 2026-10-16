package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesDelivery(t *testing.T) {
	t.Run("receiver-admission", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			got, err := sessionMessageWait(ctx, r, b.PG)
			if !errors.Is(err, context.Canceled) || got != (sessionMessage{}) {
				t.Fatal("receiver admission must return zero and cancellation", got, err)
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "exec"
		if prepared {
			name = "held-prepare"
		}
		t.Run(name, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				sender, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer sender.Close()
				r := sessionMessageConn(t, p, b)
				peer := sessionMessageConn(t, p, b)
				sessionMessageSQL(t, p, b, r, "LISTEN session_messages")
				sessionMessageSQL(t, p, b, peer, "LISTEN session_messages")
				for _, q := range []string{
					"LISTEN session_messages", "LISTEN session_messages",
					"UNLISTEN absent_session_messages", "NOTIFY session_messages, 'payload'",
					"NOTIFY session_messages", "NOTIFY session_messages, '雪☃'",
					"UNLISTEN session_messages", "UNLISTEN *",
				} {
					if !prepared {
						sessionMessageSQL(t, p, b, sender, q)
					} else {
						stmt, err := sender.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal("effect-free utility Prepare:", err)
						}
						if stmt == nil {
							t.Fatal("Prepare returned nil statement")
						}
						held := fixture.HeldStatement{Executor: sender, Statement: stmt, SQL: q}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					sessionMessageHealth(t, p, b, sender)
				}
				var senderPID uint64
				for _, payload := range []string{"payload", "", "雪☃"} {
					a := sessionMessageReceive(t, p, b, r, "session_messages", payload)
					c := sessionMessageReceive(t, p, b, peer, "session_messages", payload)
					if senderPID == 0 {
						senderPID = a.PID
					}
					if a.PID != senderPID || c.PID != senderPID {
						t.Fatal("same sender changed identity", a, c, senderPID)
					}
				}
				sessionMessageSQL(t, p, b, r, "NOTIFY session_messages, 'self'")
				a := sessionMessageReceive(t, p, b, r, "session_messages", "self")
				c := sessionMessageReceive(t, p, b, peer, "session_messages", "self")
				if a.PID != c.PID || a.PID == senderPID {
					t.Fatal("self/peer identity or physical sender distinction", a, c, senderPID)
				}
				// No additional send from Prepare itself may precede this sentinel.
				sessionMessageSQL(t, p, b, sender, "NOTIFY session_messages, 'sentinel'")
				sessionMessageReceive(t, p, b, r, "session_messages", "sentinel")
				rows, err := sender.QueryContext(p.Ctx, "NOTIFY session_messages, 'query-route'")
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil || len(columns) != 0 || rows.Next() {
					rows.Close()
					t.Fatal("utility query must have no columns or rows", columns, err)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				sessionMessageReceive(t, p, b, r, "session_messages", "query-route")
				sessionMessageHealth(t, p, b, sender)
			})
		})
	}

	t.Run("held-subscription-effects", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			sender := sessionMessageConn(t, p, b)
			runHeld := func(q string) {
				t.Helper()
				stmt, err := r.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error("statement close:", err)
					}
				}()
				held := fixture.HeldStatement{Executor: r, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			}
			for _, q := range []string{"UNLISTEN prepared_delivery", "UNLISTEN *"} {
				runHeld("LISTEN prepared_delivery")
				sessionMessageSQL(t, p, b, sender, "NOTIFY prepared_delivery, 'subscribed'")
				sessionMessageReceive(t, p, b, r, "prepared_delivery", "subscribed")
				runHeld(q)
				sessionMessageSQL(t, p, b, r, "LISTEN prepared_sentinel")
				sessionMessageSQL(t, p, b, sender, "NOTIFY prepared_delivery, 'removed'")
				sessionMessageSQL(t, p, b, sender, "NOTIFY prepared_sentinel, 'retained'")
				sessionMessageReceive(t, p, b, r, "prepared_sentinel", "retained")
			}
		})
	})

	t.Run("prepare-has-no-subscription-effects", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			sender := sessionMessageConn(t, p, b)
			stmt, err := r.PrepareContext(p.Ctx, "LISTEN never_executed")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			sessionMessageSQL(t, p, b, sender, "NOTIFY never_executed, 'must-not-arrive'")
			sessionMessageSQL(t, p, b, r, "LISTEN sentinel")
			unlisten, err := r.PrepareContext(p.Ctx, "UNLISTEN sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer unlisten.Close()
			sessionMessageSQL(t, p, b, sender, "NOTIFY sentinel, 'still-listening'")
			sessionMessageReceive(t, p, b, r, "sentinel", "still-listening")
		})
	})
}
