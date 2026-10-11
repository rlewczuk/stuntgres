package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Compose existing arbitrary-chunk and barrier helpers; partial quoted data is not EOF.
type copyCSVBlockedReader struct {
	prefix             copyTextChunks
	barrier            *copyTextBarrier
	released, finished bool
}

func (r *copyCSVBlockedReader) Read(p []byte) (int, error) {
	if r.prefix.offset < len(r.prefix.text) {
		return r.prefix.Read(p)
	}
	if !r.released {
		r.barrier.wait()
		r.released = true
	}
	// Release supplies the closing quote, not malformed EOF. Otherwise CSV
	// parsing can report 22P04 before the acknowledged server interrupt.
	// No outer separator: the completed record retains the active line Context.
	if !r.finished {
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = '"'
		r.finished = true
		return 1, nil
	}
	return 0, io.EOF
}
func copyCSVJoin(t *testing.T, done <-chan copyTextOutcome) copyTextOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("CSV worker did not join after caller release")
		return copyTextOutcome{}
	}
}

func TestReferenceCopyCSVLifecycle(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_csv_life(id int,v text)")
		c.exec(t, "INSERT INTO copy_csv_life VALUES(99,'sentinel')")
		peerDB := p.Native
		if b.PG {
			peerDB = p.CopyPostgres
		}
		peer, err := peerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, ctx: p.Ctx, pg: b.PG}
		var pid int
		if b.PG {
			if err = c.conn.QueryRowContext(c.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
		}
		for _, timeout := range []bool{false, true} {
			if timeout && !b.PG {
				continue
			} // Native Operation expiry is an explicit private-unit phase.
			if timeout {
				c.exec(t, "SET statement_timeout TO '100ms'")
				c.selectRows(t, "SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"100ms"}})
			}
			active := c
			ctx, cancel := context.WithCancel(c.ctx)
			active.ctx = ctx
			barrier := newCopyTextBarrier()
			done := make(chan copyTextOutcome, 1)
			joined := false
			func() {
				defer func() {
					cancel()
					barrier.unblock()
					if !joined {
						copyCSVJoin(t, done)
					}
				}()
				q := "COPY copy_csv_life FROM STDIN WITH(FORMAT csv)"
				go func() {
					result, err := active.from(q, &copyCSVBlockedReader{prefix: copyTextChunks{text: "1,one\n2,\"open\nfield", widths: []int{2, 1, 7}}, barrier: barrier})
					done <- copyTextOutcome{result, err}
				}()
				select {
				case <-barrier.entered:
				case out := <-done:
					joined = true
					t.Fatalf("CSV ended before barrier: %+v", out)
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				if b.PG {
					observation, stop := context.WithTimeout(p.Ctx, 5*time.Second)
					defer stop()
					for {
						var ready bool
						query := "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='ClientRead' FROM pg_stat_activity WHERE pid=$1),FALSE)"
						if timeout {
							query = "SELECT COALESCE((SELECT query=$2 AND clock_timestamp() >= query_start + INTERVAL '100 milliseconds' FROM pg_stat_activity WHERE pid=$1),FALSE)"
						}
						if err := peer.QueryRowContext(observation, query, pid, q).Scan(&ready); err != nil {
							t.Fatal(err)
						}
						if ready {
							break
						}
					}
					if !timeout {
						var signaled bool
						if err := peer.QueryRowContext(observation, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
							t.Fatal(signaled, err)
						}
					}
				} else {
					cancel()
				}
				barrier.unblock()
				out := copyCSVJoin(t, done)
				joined = true
				if b.PG {
					message := "canceling statement due to user request"
					if timeout {
						message = "canceling statement due to statement timeout"
					}
					copyTextDiagnostic(t, out.result, out.err, fixture.Diagnostic{Code: "57014", Message: message, Context: "COPY copy_csv_life, line 3: \"2,\"open\nfield\"\""})
				} else if !errors.Is(out.err, context.Canceled) || out.result != (copyTextResult{}) {
					t.Fatal(out)
				}
				observer.selectRows(t, "SELECT * FROM copy_csv_life ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
			}()
		}
		failure := errors.New("CSV writer failure")
		writer := &copyTextPrefixErrorWriter{err: failure}
		result, err := observer.to("COPY copy_csv_life TO STDOUT WITH(FORMAT csv)", writer)
		if !errors.Is(err, failure) || result != (copyTextResult{}) || writer.Len() == 0 {
			t.Fatal(result, err, writer.String())
		}
		if !b.PG {
			result, err = c.from("COPY copy_csv_life FROM STDIN WITH(FORMAT csv)", io.MultiReader(bytes.NewBufferString("1,one\n2,\"open"), &copyCSVErrorReader{failure}))
			if !errors.Is(err, failure) || result != (copyTextResult{}) {
				t.Fatal(result, err)
			}
			c.selectRows(t, "SELECT * FROM copy_csv_life", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		}
	})
}

type copyCSVErrorReader struct{ err error }

func (r *copyCSVErrorReader) Read([]byte) (int, error) { return 0, r.err }
