package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Observe real provider/server admission before cancellation. Admission has a
// deadline and cleanup has a bounded worker join on every exit, before the
// caller closes its held statements or physical connection.
func binaryConversionActiveCancel(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, query, provider string, operation func(context.Context) relationOutcome) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	pid := 0
	if b.pg {
		pid = referenceLockPID(t, ctx, c)
	}
	done := make(chan relationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			outcome := referenceLockResult(t, done)
			if outcome.rows != nil {
				outcome.rows.Close()
			}
		}
	}()
	go func() { done <- operation(ctx) }()
	profiles := make([]runtime.StackRecord, 128)
	observed := false
	for !observed && ctx.Err() == nil {
		select {
		case outcome := <-done:
			joined = true
			if outcome.rows != nil {
				outcome.rows.Close()
			}
			t.Fatal("conversion completed before active admission", query, outcome.err)
		default:
		}
		if b.pg {
			if err := referenceAdmin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1),false)", pid, query).Scan(&observed); err != nil {
				t.Fatal("conversion admission", err)
			}
		} else {
			n, ok := runtime.GoroutineProfile(profiles)
			if !ok {
				profiles = make([]runtime.StackRecord, n)
				continue
			}
			for _, profile := range profiles[:n] {
				frames := runtime.CallersFrames(profile.Stack())
				providerFrame, workerFrame := false, false
				for {
					frame, more := frames.Next()
					providerFrame = providerFrame || strings.Contains(frame.Function, provider)
					workerFrame = workerFrame || strings.Contains(frame.Function, "binaryConversionActiveCancel")
					if !more {
						break
					}
				}
				if providerFrame && workerFrame {
					observed = true
					break
				}
			}
			if !observed {
				runtime.Gosched()
			}
		}
	}
	if !observed {
		t.Fatal("conversion admission deadline", query, ctx.Err())
	}
	if b.pg {
		var signaled bool
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal("conversion backend cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	outcome := referenceLockResult(t, done)
	joined = true
	if outcome.rows != nil {
		outcome.rows.Close()
		t.Fatal("active canceled conversion published cursor")
	}
	if outcome.result != nil {
		t.Fatal("active canceled conversion published result")
	}
	if b.pg {
		referenceAssertDiagnostic(t, outcome.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else {
		lexicalOrdinary(t, outcome.err, context.Canceled)
	}
}

func TestReferenceBinaryConversionActiveCancellation(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_active_input(id integer,b bytea)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_active_out(id integer,b bytea)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO bc_active_out VALUES(0,'\xff')`, 1))
		// Both providers must scan a genuine large stored payload. Do not use a
		// substitute pg_sleep query, production delay, hook or context-check count.
		large := bytes.Repeat([]byte{0xe9}, 32<<20)
		inserted, err := c.ExecContext(p.ctx, "INSERT INTO bc_active_input VALUES(1,$1),(2,$2)", []byte{0xe9}, large)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := inserted.RowsAffected(); err != nil || n != 2 {
			t.Fatal(n, err)
		}
		readSQL := "SELECT sum(octet_length(encode(b,'hex'))) AS n FROM bc_active_input"
		writeSQL := "INSERT INTO bc_active_out(id,b) SELECT id,convert(b,'LATIN1','UTF8') FROM bc_active_input ORDER BY id"
		read, err := c.PrepareContext(p.ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := c.PrepareContext(p.ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		binaryConversionActiveCancel(t, p, b, c, readSQL, "sqltype.binaryFormatPass", func(ctx context.Context) relationOutcome {
			rows, err := read.QueryContext(ctx)
			return relationOutcome{rows: rows, err: err}
		})
		// SAME pre-held owner recovers after admitted read interruption.
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`UPDATE bc_active_input SET b='\xe9' WHERE id=2`, 1))
		heldRead := referenceHeldStatement{referenceExecutor: c, statement: read, query: readSQL}
		referenceRunStep(t, p.ctx, heldRead, b.recorder, referenceSelect(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		changed, err := c.ExecContext(p.ctx, "UPDATE bc_active_input SET b=$1 WHERE id=2", large)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := changed.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		binaryConversionActiveCancel(t, p, b, c, writeSQL, "sqltype.binaryEncodingPass", func(ctx context.Context) relationOutcome {
			result, err := write.ExecContext(ctx)
			return relationOutcome{result: result, err: err}
		})
		// The small first row precedes the large conversion in the ordered staged
		// write; interruption must publish neither it nor any partial large value.
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,b FROM bc_active_out ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`UPDATE bc_active_input SET b='\xe9' WHERE id=2`, 1))
		referenceRunStep(t, p.ctx, heldRead, b.recorder, referenceSelect(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: write, query: writeSQL}, b.recorder, referenceExec(writeSQL, 2))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,b FROM bc_active_out ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "\xff"}, {int64(1), "é"}, {int64(2), "é"}}))
	})
}
