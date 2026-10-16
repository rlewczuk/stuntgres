package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSessionMessagesRecipientBoundaries(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("pending-new-channel-"+end, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				r := sessionMessageConn(t, p, b)
				s := sessionMessageConn(t, p, b)
				for _, q := range []string{"LISTEN a", "BEGIN"} {
					sessionMessageSQL(t, p, b, r, q)
				}
				sessionMessageSQL(t, p, b, s, "NOTIFY b, 'before-listen-b'")
				sessionMessageSQL(t, p, b, r, "LISTEN b")
				sessionMessageSQL(t, p, b, r, end)
				sessionMessageSQL(t, p, b, s, "NOTIFY a, 'sentinel'")
				if end == "COMMIT" {
					// Source-derived expectation, specifically awaiting PG18 oracle.
					sessionMessageReceive(t, p, b, r, "b", "before-listen-b")
				}
				sessionMessageReceive(t, p, b, r, "a", "sentinel")
			})
		})
	}

	for _, action := range []string{"UNLISTEN a", "UNLISTEN *", "UNLISTEN a; LISTEN a; UNLISTEN a"} {
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(action+"-"+end, func(t *testing.T) {
				sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					r := sessionMessageConn(t, p, b)
					s := sessionMessageConn(t, p, b)
					for _, q := range []string{"LISTEN a", "BEGIN"} {
						sessionMessageSQL(t, p, b, r, q)
					}
					sessionMessageSQL(t, p, b, s, "NOTIFY a, 'pending'")
					sessionMessageSQL(t, p, b, r, action)
					sessionMessageSQL(t, p, b, r, end)
					sessionMessageSQL(t, p, b, r, "LISTEN sentinel")
					sessionMessageSQL(t, p, b, s, "NOTIFY sentinel, 'end'")
					if end == "ROLLBACK" {
						sessionMessageReceive(t, p, b, r, "a", "pending")
					}
					sessionMessageReceive(t, p, b, r, "sentinel", "end")
				})
			})
		}
	}

	t.Run("first-listen-no-backfill", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			r := sessionMessageConn(t, p, b)
			s := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, r, "BEGIN")
			sessionMessageSQL(t, p, b, s, "NOTIFY a, 'old'")
			sessionMessageSQL(t, p, b, r, "LISTEN a")
			sessionMessageSQL(t, p, b, r, "COMMIT")
			sessionMessageSQL(t, p, b, s, "NOTIFY a, 'new'")
			sessionMessageReceive(t, p, b, r, "a", "new")
		})
	})

	for _, clear := range []string{"UNLISTEN a", "UNLISTEN *", "DISCARD ALL"} {
		t.Run("ready-survives-"+clear, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				r := sessionMessageConn(t, p, b)
				s := sessionMessageConn(t, p, b)
				sessionMessageSQL(t, p, b, r, "LISTEN a")
				sessionMessageSQL(t, p, b, s, "NOTIFY a, 'ready'")
				// Command-response processing buffers notifications before the
				// following clear, unlike server-pending data inside BEGIN.
				sessionMessageSQL(t, p, b, r, "LISTEN sentinel")
				sessionMessageSQL(t, p, b, r, clear)
				sessionMessageReceive(t, p, b, r, "a", "ready")
				sessionMessageSQL(t, p, b, s, "NOTIFY a, 'removed'")
				sessionMessageSQL(t, p, b, r, "LISTEN sentinel")
				sessionMessageSQL(t, p, b, s, "NOTIFY sentinel, 'only'")
				sessionMessageReceive(t, p, b, r, "sentinel", "only")
			})
		})
	}

	for _, boundary := range []string{
		"BEGIN", "SELECT 1/0", "ROLLBACK TO child", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "COMMIT; BEGIN",
	} {
		t.Run("withheld-after-"+boundary, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				r := sessionMessageConn(t, p, b)
				s := sessionMessageConn(t, p, b)
				for _, q := range []string{"LISTEN a", "BEGIN", "SAVEPOINT child"} {
					sessionMessageSQL(t, p, b, r, q)
				}
				sessionMessageSQL(t, p, b, s, "NOTIFY a, 'pending'")
				if boundary == "SELECT 1/0" {
					fixture.RunStep(t, p.Ctx, r, b.Recorder, fixture.Error(boundary, "22012"))
				} else if boundary != "BEGIN" {
					sessionMessageSQL(t, p, b, r, boundary)
				}
				// CHAIN/internal COMMIT-BEGIN are explicit oracle probes: a
				// returned event is reported, not silently accepted as parity.
				sessionMessageAbsent(t, p, b, r)
			})
		})
	}

	for _, end := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("aborted-outer-end-"+end, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				r := sessionMessageConn(t, p, b)
				s := sessionMessageConn(t, p, b)
				sessionMessageSQL(t, p, b, r, "LISTEN a")
				sessionMessageSQL(t, p, b, r, "BEGIN")
				sessionMessageSQL(t, p, b, s, "NOTIFY a, 'released'")
				fixture.RunStep(t, p.Ctx, r, b.Recorder, fixture.Error("SELECT 1/0", "22012"))
				sessionMessageSQL(t, p, b, r, end)
				sessionMessageReceive(t, p, b, r, "a", "released")
			})
		})
	}
}
