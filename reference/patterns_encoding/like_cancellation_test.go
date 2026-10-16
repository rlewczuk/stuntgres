package reference_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func likeReferenceCancelEntered(t *testing.T, provider string, operation func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- operation(ctx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
				joined = true
			case <-time.After(5 * time.Second):
				t.Error("canceled worker did not complete during cleanup", provider)
			}
		}
	}()
	// Follow the shared fixture's complete-live-stack observation convention.
	// Matching still requires this provider and this worker in one goroutine.
	stacks := make([]byte, 64<<10)
	providerSymbol := []byte(provider)
	workerSymbol := []byte("likeReferenceCancelEntered.func1")
	var poll *time.Timer
	defer func() {
		if poll != nil {
			poll.Stop()
		}
	}()
	observed := false
	for !observed && ctx.Err() == nil {
		select {
		case err := <-result:
			joined = true
			t.Fatal("operation completed before provider observation", err)
		default:
		}
		n := runtime.Stack(stacks, true)
		for n == len(stacks) {
			stacks = make([]byte, len(stacks)*2)
			n = runtime.Stack(stacks, true)
		}
		remaining := stacks[:n]
		for len(remaining) != 0 {
			end := bytes.Index(remaining, []byte("\n\n"))
			if end < 0 {
				end = len(remaining)
			}
			frames := remaining[:end]
			// Creation metadata is not a live frame of this goroutine.
			if created := bytes.Index(frames, []byte("\ncreated by ")); created >= 0 {
				frames = frames[:created]
			}
			if bytes.Contains(frames, providerSymbol) && bytes.Contains(frames, workerSymbol) {
				observed = true
				break
			}
			if end == len(remaining) {
				break
			}
			remaining = remaining[end+2:]
		}
		if !observed {
			// Complete snapshots stop the world. Delay after unsuccessful
			// samples so observation does not monopolize worker progress.
			// Start a fresh delay rather than accumulating ticker events.
			if poll == nil {
				poll = time.NewTimer(time.Millisecond)
			} else {
				poll.Reset(time.Millisecond)
			}
			select {
			case <-ctx.Done():
			case err := <-result:
				joined = true
				t.Fatal("operation completed before provider observation", err)
			case <-poll.C:
			}
		}
	}
	cancel()
	var err error
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("canceled worker did not complete", provider)
	}
	if !observed || !errors.Is(err, context.Canceled) {
		t.Fatal("entered provider cancellation", provider, observed, err)
	}
}

func TestReferenceLikeHeldEnteredCancellation(t *testing.T) {
	for _, tc := range []struct{ name, provider, input, pattern, escape string }{
		{"matcher", "sqltype.matchLike", strings.Repeat("a", 200000), "%" + strings.Repeat("a", 100000) + "b", "\\"},
		{"normalizer", "sqltype.LikeEscapePlan.Evaluate", "a", strings.Repeat("é!", 1000000), "!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					db.SetMaxOpenConns(1)
					fixture.StringCoreControlExec(t, db, "CREATE TABLE t(v TEXT,p TEXT,e TEXT)")
					if _, err := db.Exec("INSERT INTO t VALUES($1,$2,$3)", tc.input, tc.pattern, tc.escape); err != nil {
						t.Fatal(err)
					}
					stmt, err := db.Prepare("SELECT v LIKE p ESCAPE e FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if backend.PG {
						c, err := db.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						// Native observes the Go LIKE provider. PostgreSQL admission is an actual
						// server command observed in pg_stat_activity; it has no Go provider frame.
						fixture.SessionCancel(t, p.Ctx, c, true)
						if err := c.Close(); err != nil {
							t.Fatal(err)
						}
					} else {
						likeReferenceCancelEntered(t, tc.provider, func(ctx context.Context) error {
							rows, err := stmt.QueryContext(ctx)
							if rows != nil {
								defer rows.Close()
								if rows.Next() {
									return errors.New("canceled provider published row")
								}
								if err == nil {
									err = rows.Err()
								}
							}
							return err
						})
					}
					fixture.StringCoreControlExec(t, db, "UPDATE t SET p='%',e='!'")
					rows, err := stmt.Query()
					fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}}, false)
				})
			}
		})
	}
}

func TestReferenceLikeHeldUpdateEnteredCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			db.SetMaxOpenConns(1)
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(v TEXT)")
			input := strings.Repeat("a", 200000)
			if _, err := db.Exec("INSERT INTO t VALUES($1)", input); err != nil {
				t.Fatal(err)
			}
			stmt, err := db.Prepare("UPDATE t SET v='changed' WHERE v LIKE $1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if backend.PG {
				c, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				// Native observes the Go LIKE provider. PostgreSQL admission is an actual
				// server command observed in pg_stat_activity; it has no Go provider frame.
				fixture.SessionCancel(t, p.Ctx, c, true)
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				likeReferenceCancelEntered(t, "sqltype.matchLike", func(ctx context.Context) error {
					_, err := stmt.ExecContext(ctx, "%"+strings.Repeat("a", 100000)+"b")
					return err
				})
			}
			var value string
			if err := db.QueryRow("SELECT v FROM t").Scan(&value); err != nil || value != input {
				t.Fatal("canceled predicate published update", len(value), err)
			}
			r, err := stmt.Exec("%")
			if err != nil {
				t.Fatal("fresh held frame", err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if err := db.QueryRow("SELECT v FROM t").Scan(&value); err != nil || value != "changed" {
				t.Fatal(value, err)
			}
		})
	}
}
