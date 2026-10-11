package reference_test

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/moby/moby/client"
	"io"
	"strings"
	"stuntgres/reference/fixture"
	"sync"
	"time"
)

// This instrumentation belongs only to the release oracle. It never synthesizes
// a pq diagnostic: pq intentionally translates FATAL to driver.ErrBadConn.
// PostgreSQL log_line_prefix %p/%c/%e supplies real backend incarnation/SQLSTATE,
// separately from exported API sentinels and native portable diagnostic fields.
func (f *timeoutReferenceFixture) captureOwner() {
	f.t.Helper()
	if !f.b.PG {
		return
	}
	ctx, cancel := context.WithTimeout(f.p.Ctx, 2*time.Second)
	defer cancel()
	var backendStart time.Time
	// PostgreSQL documents this %c derivation. Capture before any timer is armed;
	// PID plus session ID distinguishes process incarnations despite log overlap.
	if err := f.owner.QueryRowContext(ctx, "SELECT pid,backend_start,to_hex(trunc(EXTRACT(EPOCH FROM backend_start))::integer)||'.'||to_hex(pid) FROM pg_stat_activity WHERE pid=pg_backend_pid()").Scan(&f.ownerPID, &backendStart, &f.ownerSessionID); err != nil {
		f.t.Fatal("capturing pinned PostgreSQL backend incarnation", err)
	}
	if f.ownerPID <= 0 || backendStart.IsZero() || f.ownerSessionID == "" {
		f.t.Fatal("invalid pinned PostgreSQL backend incarnation")
	}
	// The daemon and PG container share a clock. One-second overlap retains the
	// boundary record; exact %c matching and the baseline prevent stale matches.
	f.logSince = backendStart.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	f.logBaseline = f.oracleLogs(ctx)
}

func (f timeoutReferenceFixture) oracleLogs(ctx context.Context) string {
	f.t.Helper()
	if fixture.LogClient == nil || fixture.ContainerID == "" || f.logSince == "" {
		f.t.Fatal("pinned PostgreSQL log capture is not initialized")
	}
	reader, err := fixture.LogClient.ContainerLogs(ctx, fixture.ContainerID, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Since: f.logSince,
	})
	if err != nil {
		f.t.Fatal("reading pinned PostgreSQL log suffix", err)
	}
	// Own the raw response, not testcontainers' per-frame pipe wrapper. Its
	// cancellation and close cover both payload and transport on every path.
	data, err := timeoutReadOracleLogs(ctx, reader, 32<<20)
	if err != nil {
		f.t.Fatal("reading bounded pinned PostgreSQL log evidence", err)
	}
	return data
}

// Decode non-TTY Docker frames synchronously, validating the cumulative wire
// cap, including headers, before reading each payload. Join cancellation's raw
// Close before returning; no demultiplexer goroutine retains response ownership.
func timeoutReadOracleLogs(ctx context.Context, body io.ReadCloser, maxWire int) (string, error) {
	var closeOnce sync.Once
	var closeErr error
	closeBody := func() { closeOnce.Do(func() { closeErr = body.Close() }) }
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeBody(); close(closed) })
	var data strings.Builder
	readErr := func() error {
		reader := bufio.NewReader(body)
		var header [8]byte
		var scratch [32 << 10]byte
		limited := io.LimitedReader{R: reader}
		remaining := int64(maxWire)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := io.ReadFull(reader, header[:])
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("Docker log frame header: %w", err)
			}
			if remaining < int64(len(header)) {
				return errors.New("pinned PostgreSQL log evidence exceeds bounded capture")
			}
			remaining -= int64(len(header))
			if header[0] != 1 && header[0] != 2 {
				return fmt.Errorf("unknown Docker log stream: %d", header[0])
			}
			if header[1] != 0 || header[2] != 0 || header[3] != 0 {
				return fmt.Errorf("invalid Docker log stream header %v", header)
			}
			length := int64(binary.BigEndian.Uint32(header[4:]))
			if length > remaining {
				return errors.New("pinned PostgreSQL log evidence exceeds bounded capture")
			}
			limited.N = length
			n, err := io.CopyBuffer(&data, &limited, scratch[:])
			if err != nil {
				return fmt.Errorf("Docker log frame payload: %w", err)
			}
			if n != length {
				return fmt.Errorf("Docker log frame payload: %w", io.ErrUnexpectedEOF)
			}
			remaining -= length
		}
	}()
	// If cancellation already dispatched Close, join it before returning.
	// Otherwise perform the single owned Close here, also on read/size errors.
	if !stop() {
		<-closed
	}
	closeBody()
	if err := errors.Join(readErr, ctx.Err(), closeErr); err != nil {
		return "", err
	}
	return data.String(), nil
}

func (f timeoutReferenceFixture) serverFatal(code string, position ...string) {
	f.t.Helper()
	want := timeoutReferenceWant(code)
	if want.Severity != "FATAL" {
		f.t.Fatal("server fatal assertion requires FATAL")
	}
	prefix := fmt.Sprintf("timeout-oracle|%d|%s|", f.ownerPID, f.ownerSessionID)
	exact := prefix + code + "|FATAL:  " + want.Message
	if len(position) != 0 && position[0] != "" {
		exact += " at character " + position[0]
	}
	// Compare the exact controlled PG prefix, including the documented %c
	// incarnation. Scoped Since reads overlap the baseline but never silently
	// tail away an expected fatal or accept a reused PID from an older session.
	if strings.Contains(f.logBaseline, exact) {
		f.t.Fatal("fatal already existed before owner was armed", exact)
	}
	ctx, cancel := context.WithTimeout(f.p.Ctx, 2*time.Second)
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
	if f.b.PG && timeoutReferenceWant(code).Severity == "FATAL" {
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
	fixture.AssertDiagnostic(f.t, err, &want)
}
