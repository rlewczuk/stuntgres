package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesPublication(t *testing.T) {
	t.Run("shared-commit-order-and-atomic-failure", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			sessionMessageSQL(t, p, b, r, "LISTEN publication")
			sessionMessageSQL(t, p, b, s, "CREATE TABLE message_publication(v INT PRIMARY KEY)")
			sessionMessageSQL(t, p, b, s, "CREATE TABLE message_catalog(v INT)")
			for _, q := range []string{
				"BEGIN", "NOTIFY publication, 'empty-first'", "COMMIT",
				"BEGIN", "INSERT INTO message_publication VALUES(1)", "NOTIFY publication, 'row-second'", "COMMIT",
				"BEGIN", "ALTER TABLE message_catalog ALTER COLUMN v SET NOT NULL", "NOTIFY publication, 'catalog-third'", "COMMIT",
			} {
				// INSERT has an independently specified affected count.
				if q == "INSERT INTO message_publication VALUES(1)" {
					fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Exec(q, 1))
				} else {
					sessionMessageSQL(t, p, b, s, q)
				}
			}
			for _, payload := range []string{"empty-first", "row-second", "catalog-third"} {
				sessionMessageReceive(t, p, b, r, "publication", payload)
			}
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error("INSERT INTO message_catalog VALUES(NULL)", "23502"))
			for _, q := range []string{"BEGIN", "NOTIFY publication, 'constraint-lost'"} {
				sessionMessageSQL(t, p, b, s, q)
			}
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error("INSERT INTO message_publication VALUES(1)", "23505"))
			// Aborted COMMIT rolls back the whole outgoing incarnation.
			sessionMessageSQL(t, p, b, s, "COMMIT")
			sessionMessageSQL(t, p, b, s, "BEGIN")
			sessionMessageSQL(t, p, b, s, "NOTIFY publication, 'catalog-lost'")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Error("ALTER TABLE message_catalog ALTER COLUMN missing SET NOT NULL", "42703"))
			sessionMessageSQL(t, p, b, s, "ROLLBACK")
			sessionMessageSQL(t, p, b, s, "NOTIFY publication, 'sentinel'")
			sessionMessageReceive(t, p, b, r, "publication", "sentinel")
			fixture.RunStep(t, p.Ctx, s, b.Recorder, fixture.Select(
				"SELECT v FROM message_publication", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if !b.PG {
				// Native admission rejects cancellation before any publication.
				// pq can complete this utility despite a pre-cancelled context.
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := s.ExecContext(ctx, "NOTIFY publication, 'cancelled'")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("precancel published a result", result, err)
				}
			}
			sessionMessageSQL(t, p, b, s, "NOTIFY publication, 'after-cancel'")
			sessionMessageReceive(t, p, b, r, "publication", "after-cancel")
		})
	})

	t.Run("serialization-discards-notify", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			r := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN publication")
			sessionMessageSQL(t, p, b, a, "CREATE TABLE message_serial(v INT)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO message_serial VALUES(1)", 1))
			sessionMessageSQL(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(
				"SELECT v FROM message_serial", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			sessionMessageSQL(t, p, b, a, "NOTIFY publication, 'serialization-lost'")
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("UPDATE message_serial SET v=2", 1))
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Error("UPDATE message_serial SET v=3", "40001"))
			sessionMessageSQL(t, p, b, a, "ROLLBACK")
			sessionMessageSQL(t, p, b, peer, "NOTIFY publication, 'survivor'")
			sessionMessageReceive(t, p, b, r, "publication", "survivor")
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(
				"SELECT v FROM message_serial", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	})

	t.Run("concurrent-identical-listener-order", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a := sessionMessageConn(t, p, b)
			c := sessionMessageConn(t, p, b)
			x := sessionMessageConn(t, p, b)
			y := sessionMessageConn(t, p, b)
			for _, r := range []*sql.Conn{a, c} {
				sessionMessageSQL(t, p, b, r, "LISTEN publication")
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
			defer cancel()
			start := make(chan struct{})
			out := make(chan error, 2)
			for i, sender := range []*sql.Conn{x, y} {
				go func() {
					<-start
					q := "NOTIFY publication, 'x'"
					if i == 1 {
						q = "NOTIFY publication, 'y'"
					}
					_, err := sender.ExecContext(ctx, q)
					out <- err
				}()
			}
			close(start)
			// Collect BOTH outcomes before failing, never guess commit order
			// from goroutine completion. Exec contexts bound all exit paths.
			firstErr, secondErr := <-out, <-out
			if firstErr != nil || secondErr != nil {
				t.Fatal("concurrent publication", firstErr, secondErr)
			}
			var order [2]sessionMessage
			for i := range order {
				got, err := sessionMessageWait(ctx, a, b.PG)
				if err != nil || got.PID == 0 || got.Channel != "publication" || (got.Payload != "x" && got.Payload != "y") {
					t.Fatal("independent event contract", got, err)
				}
				order[i] = got
			}
			if order[0].Payload == order[1].Payload || order[0].PID == order[1].PID {
				t.Fatal("missing sender or duplicate delivery", order)
			}
			for _, want := range order {
				got := sessionMessageReceive(t, p, b, c, want.Channel, want.Payload)
				if got != want {
					t.Fatal("listeners disagree on committed order", got, want)
				}
			}
		})
	})

	t.Run("wait-does-not-block-peer-commit", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN publication")
			ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
			defer cancel()
			type outcome struct {
				n   sessionMessage
				err error
			}
			out := make(chan outcome, 1)
			go func() {
				n, err := sessionMessageWait(ctx, r, b.PG)
				out <- outcome{n, err}
			}()
			// Always join the wait, including a peer SQL failure.
			_, execErr := s.ExecContext(ctx, "NOTIFY publication, 'wake'")
			if execErr != nil {
				cancel()
			}
			got := <-out
			if execErr != nil || got.err != nil || got.n.PID == 0 || got.n.Channel != "publication" || got.n.Payload != "wake" {
				t.Fatal("peer commit/wait", execErr, got)
			}
		})
	})
}
