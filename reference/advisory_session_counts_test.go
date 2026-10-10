package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestReferenceAdvisorySessionCountsCompatibility(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, holderShared := range []bool{false, true} {
					for _, requestShared := range []bool{false, true} {
						t.Run(fmt.Sprintf("space%d/holderShared%v/requestShared%v", k.space, holderShared, requestShared), func(t *testing.T) {
							hold, request, try := "pg_advisory_lock", "pg_advisory_lock", "pg_try_advisory_lock"
							if holderShared {
								hold += "_shared"
							}
							if requestShared {
								request += "_shared"
								try += "_shared"
							}
							advisorySessionValue(t, p.ctx, a, k.call(hold), "VOID", "")
							compatible := holderShared && requestShared
							advisorySessionValue(t, p.ctx, peer, k.call(try), "BOOL", compatible)
							if compatible {
								advisorySessionValue(t, p.ctx, peer, k.call(request), "VOID", "")
							} else {
								advisorySessionExec(t, p.ctx, peer, "SET lock_timeout='40ms'", 0)
								advisorySessionError(t, p.ctx, peer, k.call(request), advisorySessionTimeout("55P03", "canceling statement due to lock timeout"))
								advisorySessionExec(t, p.ctx, peer, "SET lock_timeout=0", 0)
							}
							advisorySessionClear(t, p.ctx, peer)
							// Holder unchanged after peer's compatible/failed attempt.
							advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
							advisorySessionClear(t, p.ctx, a)
						})
					}
				}
				for _, shared := range []bool{false, true} {
					acquire, try, unlock := "pg_advisory_lock", "pg_try_advisory_lock", "pg_advisory_unlock"
					if shared {
						acquire += "_shared"
						try += "_shared"
						unlock += "_shared"
					}
					advisorySessionValue(t, p.ctx, a, k.call(acquire), "VOID", "")
					advisorySessionValue(t, p.ctx, a, k.call(acquire), "VOID", "")
					advisorySessionValue(t, p.ctx, a, k.call(try), "BOOL", true)
					for i := range 3 {
						advisorySessionValue(t, p.ctx, a, k.call(unlock), "BOOL", true)
						advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", i == 2)
					}
					advisorySessionClear(t, p.ctx, peer)
				}
				// Mixed modes on one physical session have independent counters.
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock_shared"), "VOID", "")
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_lock"), "VOID", "")
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock"), "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock_shared"), "BOOL", true)
				advisorySessionExec(t, p.ctx, a, "SET lock_timeout='40ms'", 0)
				advisorySessionError(t, p.ctx, a, k.call("pg_advisory_lock"), advisorySessionTimeout("55P03", "canceling statement due to lock timeout"))
				advisorySessionExec(t, p.ctx, a, "SET lock_timeout=0", 0)
				advisorySessionClear(t, p.ctx, peer)
				advisorySessionValue(t, p.ctx, a, k.call("pg_advisory_unlock_shared"), "BOOL", true)
				advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", true)
				advisorySessionClear(t, p.ctx, peer)
			}
			// Same words, disjoint spaces; swapped pairs are also distinct.
			advisorySessionValue(t, p.ctx, a, advisorySessionKeys[0].call("pg_advisory_lock"), "VOID", "")
			advisorySessionValue(t, p.ctx, peer, advisorySessionKeys[1].call("pg_try_advisory_lock"), "BOOL", true)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_try_advisory_lock(2,1)", "BOOL", true)
			advisorySessionClear(t, p.ctx, a)
			advisorySessionClear(t, p.ctx, peer)
		})
	}
}

func TestReferenceAdvisorySessionWarnings(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			var notices, peerNotices []referenceDiagnostic
			_, a, _ := noticeReferencePool(t, p, b.pg, &notices)
			_, peer, _ := noticeReferencePool(t, p, b.pg, &peerNotices)
			for _, k := range advisorySessionKeys {
				for _, shared := range []bool{false, true} {
					unlock, hold, mode := "pg_advisory_unlock", "pg_advisory_lock", "ExclusiveLock"
					wrong := "pg_advisory_lock_shared"
					if shared {
						unlock += "_shared"
						hold += "_shared"
						mode = "ShareLock"
						wrong = "pg_advisory_lock"
					}
					want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "you don't own a lock of type " + mode}
					for _, reason := range []string{"absent", "wrong-mode", "stranger", "exhausted"} {
						for _, prepared := range []bool{false, true} {
							advisorySessionClear(t, p.ctx, a)
							advisorySessionClear(t, p.ctx, peer)
							if reason == "wrong-mode" {
								advisorySessionValue(t, p.ctx, a, k.call(wrong), "VOID", "")
							}
							if reason == "stranger" {
								advisorySessionValue(t, p.ctx, peer, k.call(hold), "VOID", "")
							}
							if reason == "exhausted" {
								advisorySessionValue(t, p.ctx, a, k.call(hold), "VOID", "")
								advisorySessionValue(t, p.ctx, a, k.call(unlock), "BOOL", true)
							}
							before, peerBefore := len(notices), len(peerNotices)
							var exec referenceExecutor = a
							var stmt *sql.Stmt
							if prepared {
								var err error
								stmt, err = a.PrepareContext(p.ctx, k.call(unlock))
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								if len(notices) != before {
									t.Fatal("Prepare emitted warning", notices)
								}
								exec = referenceHeldStatement{referenceExecutor: a, statement: stmt, query: k.call(unlock)}
							}
							advisorySessionValue(t, p.ctx, exec, k.call(unlock), "BOOL", false)
							if len(notices) != before+1 || notices[before] != want || len(peerNotices) != peerBefore {
								t.Fatal(reason, notices, peerNotices, want)
							}
							if stmt != nil {
								stmt.Close()
							}
							if reason == "wrong-mode" {
								advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", false)
							}
							if reason == "stranger" {
								advisorySessionValue(t, p.ctx, a, k.call("pg_try_advisory_lock"), "BOOL", false)
							}
						}
					}
					advisorySessionClear(t, p.ctx, a)
					advisorySessionClear(t, p.ctx, peer)
					for _, threshold := range []string{"warning", "error"} {
						advisorySessionExec(t, p.ctx, a, "SET client_min_messages="+threshold, 0)
						before := len(notices)
						advisorySessionValue(t, p.ctx, a, k.call(unlock), "BOOL", false)
						count := 0
						if threshold == "warning" {
							count = 1
						}
						if len(notices) != before+count {
							t.Fatal(threshold, notices)
						}
					}
					advisorySessionExec(t, p.ctx, a, "SET client_min_messages=notice", 0)
					advisorySessionExec(t, p.ctx, a, "BEGIN", 0)
					advisorySessionValue(t, p.ctx, a, k.call(unlock), "BOOL", false)
					advisorySessionValue(t, p.ctx, a, "SELECT true", "BOOL", true)
					advisorySessionExec(t, p.ctx, a, "ROLLBACK", 0)
				}
			}
			before := len(notices)
			advisorySessionClear(t, p.ctx, a)
			advisorySessionClear(t, p.ctx, a)
			advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_unlock(NULL::bigint)", "BOOL", nil)
			if len(notices) != before {
				t.Fatal("empty/NULL emitted warning", notices)
			}
			if !b.pg {
				// Conn.Raw owns database/sql's driver-connection mutex. Reenter
				// its actual QueryerContext directly within that public Raw
				// lifetime, not sql.Conn.QueryRow (which reacquires the mutex).
				// pq invokes handlers during protocol reads and has no such
				// native adapter reentry guarantee.
				queryBool := func(queryer driver.QueryerContext, ctx context.Context, query string, want bool) (err error) {
					rows, err := queryer.QueryContext(ctx, query, nil)
					if err != nil {
						if rows != nil {
							_ = rows.Close()
						}
						return err
					}
					if rows == nil {
						return fmt.Errorf("%s returned nil driver rows", query)
					}
					defer func() {
						if closeErr := rows.Close(); err == nil {
							err = closeErr
						}
					}()
					if len(rows.Columns()) != 1 {
						return fmt.Errorf("%s returned columns %v", query, rows.Columns())
					}
					var values [1]driver.Value
					if err = rows.Next(values[:]); err != nil {
						return err
					}
					if got, ok := values[0].(bool); !ok || got != want {
						return fmt.Errorf("%s returned %#v, want %v", query, values[0], want)
					}
					if err = rows.Next(values[:]); err != io.EOF {
						if err != nil {
							return err
						}
						return fmt.Errorf("%s returned an extra driver row", query)
					}
					return nil
				}
				var callback []referenceDiagnostic
				var owner *sql.Conn
				var physical driver.QueryerContext
				var callbackErr error
				joined := false
				_, owner, _ = noticeReferencePool(t, p, false, &callback, noticeReferenceOption{
					joined: func() bool { return joined },
					handler: func(referenceDiagnostic) {
						ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
						defer cancel()
						if physical == nil {
							callbackErr = fmt.Errorf("notice callback escaped its originating Raw lifetime")
							return
						}
						callbackErr = queryBool(physical, ctx, "SELECT true", true)
					},
				})
				ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				t.Cleanup(func() {
					cancel()
					if !joined {
						select {
						case <-done:
							joined = true
						case <-time.After(10 * time.Second):
							t.Error("callback worker cleanup did not join")
						}
					}
				})
				go func() {
					done <- owner.Raw(func(raw any) error {
						queryer, ok := raw.(driver.QueryerContext)
						if !ok {
							return fmt.Errorf("originating physical connection lacks QueryerContext: %T", raw)
						}
						physical = queryer
						defer func() { physical = nil }()
						return queryBool(queryer, ctx, "SELECT pg_advisory_unlock(999)", false)
					})
				}()
				select {
				case err := <-done:
					joined = true
					if err != nil || callbackErr != nil || len(callback) != 1 {
						t.Fatal(err, callbackErr, callback)
					}
				case <-ctx.Done():
					t.Fatal("callback reentry did not finish")
				}
			}
		})
	}
}
