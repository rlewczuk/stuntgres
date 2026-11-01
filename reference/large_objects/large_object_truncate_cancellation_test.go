package reference_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectTruncateCancellation(t *testing.T) {
	truncateScenario(t, nil, func(s *truncateSession) {
		defer s.cleanup(928311)
		s.create(928311, "616263646566")
		s.run(fixture.Exec("BEGIN", 0))
		fd := s.open(928311, truncateWrite, 0)
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			if s.b.PG {
				// The truncate result is demanded to construct pg_sleep's input.
				// Admission observes this supplied statement, not another sleep.
				q := fmt.Sprintf("SELECT pg_sleep(30+%s(%d,2))", name, fd)
				s.run(fixture.Exec("SAVEPOINT truncate_cancel", 0))
				fixture.SessionCancelQuery(s.t, s.p.Ctx, s.c, s.prepared, q, false)
				s.run(fixture.Exec("ROLLBACK TO SAVEPOINT truncate_cancel", 0))
				s.run(fixture.Exec("RELEASE SAVEPOINT truncate_cancel", 0))
			} else {
				step := truncateCall(name, fd, 2)
				_, stmt := s.held(step.Query)
				func() {
					defer stmt.Close()
					for _, held := range []bool{false, true} {
						for _, expired := range []bool{false, true} {
							var ctx context.Context
							var cancel context.CancelFunc
							want := context.Canceled
							if expired {
								ctx, cancel = context.WithDeadline(s.p.Ctx, time.Unix(0, 0))
								want = context.DeadlineExceeded
							} else {
								ctx, cancel = context.WithCancel(s.p.Ctx)
								cancel()
							}
							var err error
							if held {
								result, e := stmt.ExecContext(ctx, step.Args...)
								err = e
								if result != nil { cancel(); s.t.Fatal("canceled held execution exposed a result") }
							} else {
								result, e := s.c.ExecContext(ctx, step.Query, step.Args...)
								err = e
								if result != nil { cancel(); s.t.Fatal("canceled direct execution exposed a result") }
							}
							if !errors.Is(err, want) { cancel(); s.t.Fatalf("held=%t expired=%t: got %v, want %v", held, expired, err, want) }
							<-ctx.Done()
							terminal := ctx.Err()
							cancel()
							if terminal != want { s.t.Fatalf("terminal context %v, want %v", terminal, want) }
							s.bytes(928311, 0, 20, "616263646566")
							s.bytes(928311, 6, 1, "")
							s.pages(928311, []any{int64(0), int64(6), "616263646566"})
						}
					}
				}()
			}
			s.bytes(928311, 0, 20, "616263646566")
			s.bytes(928311, 6, 1, "")
			s.pages(928311, []any{int64(0), int64(6), "616263646566"})
		}
		s.read(fd, 6, "616263646566")
		s.run(fixture.Exec("COMMIT", 0))
		s.bytes(928311, 0, 20, "616263646566")
	})
}
