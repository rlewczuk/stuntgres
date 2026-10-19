//go:build !noservertests

package reference_test

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceServerFailedPortalIdentity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_failed_identity(v integer UNIQUE); INSERT INTO server_failed_identity VALUES (0)"), "")
			for _, cancel := range []bool{false, true} {
				name := "row_error"
				if cancel {
					name = "active_copy_cancel"
				}
				t.Run(name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, w.query(t, "BEGIN"), "")
					source := "SELECT 10/v FROM server_failed_identity"
					if cancel {
						source = "COPY server_failed_identity FROM STDIN"
					}
					serverWantTags(t, serverCycle(t, w, serverParse("source", source), serverParse("safe", "SELECT 42"), serverBind("p", "source", nil, nil)), "112Z")
					// The failed owner predates mark; its name survives, unlike the newer owner.
					serverReviewMark(t, w)
					serverWantTags(t, serverCycle(t, w, serverBind("newer", "safe", nil, nil)), "2Z")
					var frames []serverFrame
					if cancel {
						ctx, stop := context.WithTimeout(f.Ctx, 10*time.Second)
						defer stop()
						observer := f.RawPool(t, b)
						holder := serverAuthenticated(t, dsn)
						serverWantCode(t, holder.query(t, "BEGIN; INSERT INTO server_failed_identity VALUES (1)"), "")
						holding := true
						defer func() {
							if holding {
								serverWantCode(t, holder.query(t, "ROLLBACK"), "")
							}
						}()
						// Native startup cancellation keys are not logical lock PIDs.
						// Before COPY starts, this private fixture has only one writer.
						// Capture its public XID and owner rather than inventing a PID.
						var holderPID uint32
						if b.PG {
							holderPID = holder.pid
						}
						owners, err := observer.QueryContext(ctx, `SELECT pid,transactionid::text
							FROM pg_locks WHERE locktype='transactionid'
							AND mode='ExclusiveLock' AND granted
							AND ($1::integer=0 OR pid=$1::integer)`, holderPID)
						if err != nil {
							t.Fatal("COPY blocker ownership", err)
						}
						defer owners.Close()
						var owner int
						var xid string
						if !owners.Next() {
							t.Fatal("COPY blocker lacks XID", owners.Err())
						}
						if err := owners.Scan(&owner, &xid); err != nil {
							t.Fatal("COPY blocker XID", err)
						}
						if owners.Next() || owners.Err() != nil {
							t.Fatal("COPY blocker ownership is not unique", owners.Err())
						}
						if err := owners.Close(); err != nil {
							t.Fatal(err)
						}
						w.send(t, serverExecute("p", 0))
						w.send(t, serverPacket('H', nil))
						response := w.frame(t)
						if response.tag != 'G' || string(response.body) != "\x00\x00\x01\x00\x00" {
							t.Fatalf("active COPY response %c %x", response.tag, response.body)
						}
						// COPY buffers up to 1000 tuples before insertion. Fill that
						// existing batch, then observe row 1 waiting on this writer's
						// exact XID. No later duplicate row can be reached while the
						// first claim is blocked; every field is valid integer input.
						w.send(t, serverPacket('d', []byte(strings.Repeat("1\n", 1000))))
						waiter := uint32(0)
						if b.PG {
							waiter = w.pid
						}
						for {
							var waiting bool
							err := observer.QueryRowContext(ctx, `SELECT count(*)=1 FROM pg_locks
								WHERE locktype='transactionid' AND mode='ShareLock' AND NOT granted
								AND transactionid=$1::xid AND ($2::integer=0 OR pid=$2::integer)
								AND EXISTS (SELECT 1 FROM pg_locks WHERE pid=$3::integer
									AND transactionid=$1::xid AND locktype='transactionid'
									AND mode='ExclusiveLock' AND granted)`, xid, waiter, owner).Scan(&waiting)
							if err != nil {
								t.Fatal("COPY first-row unique-claim barrier", err)
							}
							if waiting {
								if b.PG {
									if err := observer.QueryRowContext(ctx, "SELECT $2::integer=ANY(pg_blocking_pids($1::integer))", w.pid, holder.pid).Scan(&waiting); err != nil {
										t.Fatal("COPY blocker edge", err)
									}
									if !waiting {
										t.Fatal("COPY claim wait lacks known blocker")
									}
								}
								break
							}
							runtime.Gosched()
						}
						stop()
						// Cancel only the admitted insertion, not the frontend-read
						// wait. The real ErrorResponse joins executor cancellation
						// before either late termination or blocker release.
						serverCancel(t, dsn, w.pid, w.key)
						frames = serverThrough(t, w, 'E')
						serverWantTags(t, frames, "E")
						serverWantCode(t, frames, "57014")
						// Only after the primary failure may late COPY termination
						// and the future Sync complete extended recovery.
						w.send(t, serverPacket('c', nil))
						w.send(t, serverPacket('S', nil))
						frames = append(frames, w.ready(t)...)
						wantContext := "COPY server_failed_identity, line 1"
						if b.PG {
							// PostgreSQL exposes the attempted heap tuple's physical
							// address, which is not visible through another session's
							// MVCC snapshot. Validate its exact grammar, not an allocator
							// slot constant; native has no physical index-tuple context.
							actual := serverFields(frames[0].body)['W']
							if !regexp.MustCompile(`^while inserting index tuple \([0-9]+,[1-9][0-9]*\) in relation "server_failed_identity_v_key"\nCOPY server_failed_identity, line 1$`).MatchString(actual) {
								t.Fatalf("COPY insertion context %q", actual)
							}
							wantContext = actual
						}
						serverNotificationError(t, frames, "57014", "canceling statement due to user request", "", wantContext)
						serverWantCode(t, holder.query(t, "ROLLBACK"), "")
						holding = false
					} else {
						frames = serverCycle(t, w, serverExecute("p", 0))
						serverNotificationError(t, frames, "22012", "division by zero", "")
					}
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'E')
					serverWantCompletions(t, frames)
					serverReviewRestore(t, w)
					if cancel {
						// COPY's lock was acquired after mark. Its executor must release it while
						// the older failed portal identity and outer transaction still exist.
						serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
						serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_failed_identity IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
					}
					frames = serverCycle(t, w, serverExecute("p", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "55000", "portal \"p\" cannot be run", "")
					serverWantReady(t, frames, 'E')
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverBind("p", "safe", nil, nil))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "42P03", "cursor \"p\" already exists", "")
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverExecute("newer", 0))
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, "34000", "portal \"newer\" does not exist", "")
					serverReviewRestore(t, w)
					frames = serverCycle(t, w, serverClose('P', "p"), serverBind("p", "safe", nil, nil), serverExecute("p", 0))
					serverWantTags(t, frames, "32DCZ")
					serverWantValues(t, frames, [][]string{{"42"}})
					serverWantReady(t, frames, 'T')
					serverScalar(t, peer, "SELECT count(*) FROM server_failed_identity", "1")
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					// Plain SELECT's legitimate Bind-time relation lock belongs to the outer
					// transaction; it must not be confused with leaked failed-executor state.
					serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
					serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_failed_identity IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
					serverScalar(t, peer, "SELECT v FROM server_failed_identity", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
