package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// This instrumentation belongs only to the release oracle. It never synthesizes
// a pq diagnostic: pq intentionally translates FATAL to driver.ErrBadConn.
// PostgreSQL log_line_prefix %p/%e supplies real server PID/SQLSTATE, separately
// from the exact exported API sentinel and native portable diagnostic fields.
func (f *timeoutReferenceFixture) captureOwner() {
	f.t.Helper()
	if !f.b.pg {
		return
	}
	f.ownerPID = referenceLockPID(f.t, f.p.ctx, f.owner)
	ctx, cancel := context.WithTimeout(f.p.ctx, 2*time.Second)
	defer cancel()
	f.logBaseline = f.oracleLogs(ctx)
}

func (f timeoutReferenceFixture) oracleLogs(ctx context.Context) string {
	f.t.Helper()
	reader, err := referenceContainer.Logs(ctx)
	if err != nil {
		f.t.Fatal("reading pinned PostgreSQL logs", err)
	}
	// Explicit cap fails rather than silently missing evidence after truncation.
	const maximum = 32 << 20
	data, readErr := io.ReadAll(io.LimitReader(reader, maximum+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		f.t.Fatal("closing pinned PostgreSQL logs", readErr, closeErr)
	}
	if len(data) > maximum {
		f.t.Fatal("pinned PostgreSQL log evidence exceeds bounded capture")
	}
	return string(data)
}

func (f timeoutReferenceFixture) serverFatal(code string, position ...string) {
	f.t.Helper()
	want := timeoutReferenceWant(code)
	if want.Severity != "FATAL" {
		f.t.Fatal("server fatal assertion requires FATAL")
	}
	prefix := fmt.Sprintf("timeout-oracle|%d|", f.ownerPID)
	exact := prefix + code + "|FATAL:  " + want.Message
	if len(position) != 0 && position[0] != "" {
		exact += " at character " + position[0]
	}
	// A per-incarnation baseline prevents a stale process-ID match. Docker's
	// stream headers may precede the log text, so compare the entire suffix
	// from our controlled prefix rather than pretending those are PG fields.
	if strings.Contains(f.logBaseline, exact) {
		f.t.Fatal("fatal already existed before owner was armed", exact)
	}
	ctx, cancel := context.WithTimeout(f.p.ctx, 2*time.Second)
	defer cancel()
	for {
		logs := f.oracleLogs(ctx)
		for _, line := range strings.Split(logs, "\n") {
			at := strings.Index(line, prefix)
			if at < 0 {
				continue
			}
			observed := strings.TrimSuffix(line[at:], "\r")
			if strings.Contains(observed, "|FATAL:") {
				if observed != exact {
					f.t.Fatalf("real server fatal = %q, want %q", observed, exact)
				}
				return
			}
		}
		timeoutReferenceIdle(f.t, ctx, 10*time.Millisecond)
	}
}

// phase[0] selects the public transport route; phase[1], when supplied, is
// the independently specified analysis Position, never parsed from logs.
func (f timeoutReferenceFixture) diagnostic(err error, code string, phase ...string) {
	f.t.Helper()
	if f.b.pg && timeoutReferenceWant(code).Severity == "FATAL" {
		want := error(driver.ErrBadConn)
		if len(phase) != 0 && phase[0] == "held" {
			want = sql.ErrConnDone
		}
		if !errors.Is(err, want) {
			f.t.Fatalf("pq %s transport = %T %v, want %v", code, err, err, want)
		}
		if len(phase) > 1 {
			f.serverFatal(code, phase[1])
		} else {
			f.serverFatal(code)
		}
		return
	}
	want := timeoutReferenceWant(code)
	if len(phase) > 1 {
		want.Position = phase[1]
	}
	referenceAssertDiagnostic(f.t, err, &want)
}
