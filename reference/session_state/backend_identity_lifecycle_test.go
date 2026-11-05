package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceBackendIdentityLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		admin := statActivityConn(t, p, b)
		peer := statActivityConn(t, p, b)
		before := identityMapped(t, p, b, o, peer)
		pid := before[1].(int64)
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
		reborrowed := statActivityConn(t, p, b)
		if got := statActivityPID(t, p, reborrowed); got != pid {
			t.Fatal("borrower return retired physical backend", got, pid)
		}
		identityEqual(t, identityMapped(t, p, b, o, reborrowed), before)
		s, err := o.PrepareContext(p.Ctx, identitySetSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identitySetSQL}
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		identityEqual(t, identityForPID(t, identitySet(t, p, b, e), pid), before)
		// Worker owns its OTHER physical connection. Requests are deterministic
		// phase barriers, not sleeps; each request overlaps retained Execute.
		type result struct {
			pid int64
			err error
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		requests := make(chan string)
		results := make(chan result, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			var c *sql.Conn
			defer func() {
				if c != nil {
					c.Close()
				}
			}()
			var newPID int64
			for {
				select {
				case <-ctx.Done():
					return
				case phase := <-requests:
					var err error
					switch phase {
					case "birth":
						c, err = b.DB.Conn(ctx)
						if err == nil {
							err = c.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&newPID)
						}
					case "reset":
						_, err = c.ExecContext(ctx, "SELECT pg_stat_reset_backend_stats($1)", newPID)
					case "retire":
						err = c.Raw(func(any) error { return driver.ErrBadConn })
						if errors.Is(err, driver.ErrBadConn) {
							err = nil
						}
						c.Close()
						c = nil
					}
					select {
					case results <- result{newPID, err}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-done }()
		var newPID int64
		for _, phase := range []string{"birth", "reset", "retire"} {
			select {
			case requests <- phase:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			identityEqual(t, identityForPID(t, identitySet(t, p, b, e), pid), before)
			var out result
			select {
			case out = <-results:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			if out.err != nil {
				t.Fatal("peer worker", phase, out.err)
			}
			newPID = out.pid
			identityPIDAbsent(t, identitySet(t, p, b, e), newPID)
			if phase != "retire" {
				statActivityClear(t, p, admin)
				identityForPID(t, identitySet(t, p, b, admin), newPID)
			} else {
				statActivityRetired(t, p, admin, newPID)
			}
		}
		cancel()
		<-done
		identityEqual(t, identityForPID(t, identitySet(t, p, b, e), pid), before)
		activityExec(t, p, o, "COMMIT")
		identityPIDAbsent(t, identitySet(t, p, b, e), newPID)
		activityExec(t, p, o, "CREATE TABLE bi_lifecycle_sentinel(v integer)")
		activityExec(t, p, o, "INSERT INTO bi_lifecycle_sentinel VALUES(37)")
		identityCancellation(t, p, b, before)
	})
}

func identityCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, target []any) {
	t.Helper()
	observer := statActivityConn(t, p, b)
	locker := statActivityConn(t, p, b)
	names := append([]string{"pg_stat_get_backend_idset"}, identityFunctions...)
	for i, name := range names {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", name, prepared), func(t *testing.T) {
				c := statActivityConn(t, p, b)
				defer c.Close()
				readerPID := statActivityPID(t, p, c)
				query := "SELECT " + name + "($1) AS value FROM bi_lifecycle_sentinel WHERE v=37"
				args := []any{target[0]}
				typ := "INT4"
				if i == 0 {
					query = "SELECT " + name + "() AS value FROM bi_lifecycle_sentinel WHERE v=37"
					args = nil
				} else {
					typ = identityTypes[i-1]
				}
				var e fixture.Executor = c
				if prepared {
					s, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					e = fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
				}
				if !b.PG {
					for _, expired := range []bool{false, true} {
						ctx, cancel := context.WithCancel(p.Ctx)
						want := context.Canceled
						if expired {
							cancel()
							ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
							want = context.DeadlineExceeded
						} else {
							cancel()
						}
						r, err := e.QueryContext(ctx, query, args...)
						cancel()
						if r != nil {
							r.Close()
							t.Fatal("canceled native query published rows")
						}
						if !errors.Is(err, want) {
							t.Fatal("ordinary context error", err, want)
						}
					}
				}
				tx, err := locker.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE bi_lifecycle_sentinel IN ACCESS EXCLUSIVE MODE", 0)
				ctx, cancel := context.WithCancel(p.Ctx)
				published := false
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, err := e.QueryContext(ctx, query, args...)
					if r != nil {
						published = r.Next()
						if err == nil {
							err = r.Err()
						}
						if closeErr := r.Close(); err == nil {
							err = closeErr
						}
					}
					return fixture.RelationOutcome{Err: err}
				})
				joined := false
				defer func() {
					cancel()
					if !joined {
						await()
					}
				}()
				statActivityBlocker(t, p, observer, readerPID, statActivityPID(t, p, locker))
				waiting := statActivityAwait(t, p, observer, readerPID, "relation")
				if waiting.text(14) != "Lock" || waiting.text(20) != query {
					t.Fatal("real admitted identity statement", waiting)
				}
				statActivitySignal(t, p, observer, readerPID, false)
				out := await()
				joined = true
				fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				if published {
					t.Fatal("canceled query published a partial result")
				}
				if n := backendCount(t, p, observer, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", readerPID); n != 0 {
					t.Fatal("canceled source retained lock ownership", n)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if statActivityPID(t, p, c) != readerPID {
					t.Fatal("SQL cancellation replaced physical owner")
				}
				r, err := e.QueryContext(p.Ctx, query, args...)
				if err != nil {
					t.Fatal("same held statement recovery", err)
				}
				rows := identityRows(t, r, b.PG, []string{"value"}, []string{typ})
				if i == 0 {
					found := false
					for _, row := range rows {
						found = found || row[0] == target[0]
					}
					if !found {
						t.Fatal("healthy idset lost target", rows)
					}
				} else if len(rows) != 1 || fmt.Sprint(fixture.Normalize(rows[0][0])) != fmt.Sprint(fixture.Normalize(target[i])) {
					t.Fatal("healthy scalar lost target identity", rows, target)
				}
				statActivityClear(t, p, observer)
				identityEqual(t, identityLookup(t, p, b, observer, target[0]), target)
			})
		}
	}
}
