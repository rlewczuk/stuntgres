package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesLifecycle(t *testing.T) {
	t.Run("borrower-reuse-and-physical-retirement", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			db := p.Native
			if b.PG {
				db = p.CopyPostgres
			}
			db.SetMaxIdleConns(1)
			r := sessionMessageConn(t, p, b)
			s, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			sessionMessageSQL(t, p, b, r, "LISTEN lifetime")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'identity'")
			first := sessionMessageReceive(t, p, b, r, "lifetime", "identity")
			sessionMessageSQL(t, p, b, s, "NOTIFY lifetime, 'retained'")
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			r = sessionMessageConn(t, p, b)
			sessionMessageReceive(t, p, b, r, "lifetime", "retained")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'same-owner'")
			if got := sessionMessageReceive(t, p, b, r, "lifetime", "same-owner"); got.PID != first.PID {
				t.Fatal("borrower return changed physical owner", first, got)
			}
			db.SetMaxIdleConns(0)
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			sessionMessageSQL(t, p, b, s, "NOTIFY lifetime, 'retired'")
			r = sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN lifetime")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'new-owner'")
			if got := sessionMessageReceive(t, p, b, r, "lifetime", "new-owner"); got.PID == first.PID {
				t.Fatal("physical retirement reused notification identity", first, got)
			}
		})
	})

	t.Run("sender-close-and-discard-subsets", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN lifetime")
			sessionMessageSQL(t, p, b, s, "NOTIFY lifetime, 'sender-closed'")
			db := p.Native
			if b.PG {
				db = p.CopyPostgres
			}
			db.SetMaxIdleConns(0)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			sessionMessageReceive(t, p, b, r, "lifetime", "sender-closed")
			for _, q := range []string{"DISCARD PLANS", "DISCARD TEMP", "DISCARD SEQUENCES"} {
				sessionMessageSQL(t, p, b, r, q)
				sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'subscribed'")
				sessionMessageReceive(t, p, b, r, "lifetime", "subscribed")
			}
			sessionMessageSQL(t, p, b, r, "BEGIN")
			want := fixture.ControlsReferenceDiagnostic("25001", "DISCARD ALL cannot run inside a transaction block")
			fixture.RunStep(t, p.Ctx, r, b.Recorder, fixture.ErrorDiagnostic("DISCARD ALL", want))
			sessionMessageSQL(t, p, b, r, "ROLLBACK")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'after-error'")
			sessionMessageReceive(t, p, b, r, "lifetime", "after-error")
		})
	})

	t.Run("timeout-same-physical-reuse", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN lifetime")
			sessionMessageAbsent(t, p, b, r)
			// Both pinned PG18 and native waits preserve this exact physical
			// connection after timeout; do not replace its borrower.
			if err := r.PingContext(p.Ctx); err != nil {
				t.Fatal("timeout poisoned receiver:", err)
			}
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'after-timeout'")
			sessionMessageReceive(t, p, b, r, "lifetime", "after-timeout")
		})
	})

	t.Run("native-controls-isolation-and-cancellation", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			if b.PG {
				return // Native controls and cancellation precedence have no PG API equivalent.
			}
			r := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "LISTEN lifetime")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'identity'")
			identity := sessionMessageReceive(t, p, b, r, "lifetime", "identity")
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'dump-excluded'")
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			sessionMessageReceive(t, p, b, r, "lifetime", "dump-excluded")
			for _, replace := range []func() error{
				func() error { return stuntgres.Reset(p.Ctx, p.Native) },
				func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())) },
			} {
				sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'survives'")
				if err := replace(); err != nil {
					t.Fatal(err)
				}
				if got := sessionMessageReceive(t, p, b, r, "lifetime", "survives"); got.PID != identity.PID {
					t.Fatal("replacement changed physical identity", got, identity)
				}
			}
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'cancel-preserved'")
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			got, err := sessionMessageWait(ctx, r, false)
			if !errors.Is(err, context.Canceled) || got != (sessionMessage{}) {
				t.Fatal("precancel consumed ready data", got, err)
			}
			sessionMessageReceive(t, p, b, r, "lifetime", "cancel-preserved")
			sessionMessageAbsent(t, p, b, r)
			sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'after-deadline'")
			sessionMessageReceive(t, p, b, r, "lifetime", "after-deadline")

			for _, isolated := range []bool{false, true} {
				driver := p.NativeConnector.Driver().(*stuntgres.Driver)
				dsn := "notification-distinct-dsn"
				if isolated {
					driver = stuntgres.NewDriver()
					dsn = "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
				}
				connector, err := driver.OpenConnector(dsn)
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				t.Cleanup(func() { _ = db.Close() })
				if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				c, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = c.Close() })
				// Restore must not import the source's live subscription.
				sessionMessageSQL(t, p, b, c, "NOTIFY lifetime, 'not-imported'")
				sessionMessageSQL(t, p, b, c, "LISTEN sentinel")
				sessionMessageSQL(t, p, b, c, "NOTIFY sentinel, 'fresh'")
				sessionMessageReceive(t, p, b, c, "sentinel", "fresh")
				sessionMessageSQL(t, p, b, c, "LISTEN lifetime")
				sessionMessageSQL(t, p, b, r, "NOTIFY lifetime, 'foreign'")
				sessionMessageSQL(t, p, b, c, "NOTIFY lifetime, 'own'")
				sessionMessageReceive(t, p, b, c, "lifetime", "own")
			}
			ctx, stop := context.WithTimeout(p.Ctx, time.Second)
			defer stop()
			if err := r.PingContext(ctx); err != nil {
				t.Fatal(err)
			}
		})
	})
}
