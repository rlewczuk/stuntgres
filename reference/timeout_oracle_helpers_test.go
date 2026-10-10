package reference_test

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
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
	f.logBaseline = f.oracleLogs(ctx, client.ContainerLogsOptions{Tail: "1", Timestamps: true})
	var err error
	f.logSince, err = timeoutOracleBaselineTimestamp(f.logBaseline)
	if err != nil {
		f.t.Fatal("reading pinned PostgreSQL baseline timestamp", err)
	}
}

func (f timeoutReferenceFixture) oracleLogs(ctx context.Context, options client.ContainerLogsOptions) string {
	f.t.Helper()
	options.ShowStdout, options.ShowStderr = true, true
	reader, err := referenceLogClient.ContainerLogs(ctx, referenceContainerID, options)
	if err != nil {
		f.t.Fatal("reading pinned PostgreSQL logs", err)
	}
	// Own the raw response, not testcontainers' per-frame pipe wrapper. Its
	// cancellation and close cover both payload and transport on every path.
	data, err := timeoutReadOracleLogs(ctx, reader, 32<<20)
	if err != nil {
		f.t.Fatal("reading bounded pinned PostgreSQL log evidence", err)
	}
	return data
}

func timeoutOracleBaselineTimestamp(logs string) (string, error) {
	var earliest time.Time
	var result string
	for _, line := range strings.Split(logs, "\n") {
		if line == "" {
			continue
		}
		timestamp, _, ok := strings.Cut(line, " ")
		if !ok {
			return "", fmt.Errorf("baseline log record has no Docker timestamp")
		}
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return "", fmt.Errorf("baseline Docker timestamp: %w", err)
		}
		if result == "" || parsed.Before(earliest) {
			earliest, result = parsed, timestamp
		}
	}
	if result == "" {
		return "", fmt.Errorf("baseline has no timestamped Docker log record")
	}
	return result, nil
}

// Decode the non-TTY Docker frames synchronously. Validate the evidence cap
// before reading a frame payload, without allocating its advertised length.
func timeoutReadOracleLogs(ctx context.Context, body io.ReadCloser, maximum int) (string, error) {
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
			if header[0] != 1 && header[0] != 2 {
				return fmt.Errorf("unknown Docker log stream: %d", header[0])
			}
			length := int64(binary.BigEndian.Uint32(header[4:]))
			if length > int64(maximum-data.Len()) {
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
	prefix := fmt.Sprintf("timeout-oracle|%d|", f.ownerPID)
	exact := prefix + code + "|FATAL:  " + want.Message
	if len(position) != 0 && position[0] != "" {
		exact += " at character " + position[0]
	}
	// Capture only the last pre-arming record, then use its Docker-server
	// timestamp as an inclusive fence. Keep baseline assertions for that
	// boundary record; post-arming reads have no tail limit.
	if strings.Contains(f.logBaseline, exact) {
		f.t.Fatal("fatal already existed before owner was armed", exact)
	}
	ctx, cancel := context.WithTimeout(f.p.ctx, 2*time.Second)
	defer cancel()
	for {
		logs := f.oracleLogs(ctx, client.ContainerLogsOptions{Since: f.logSince})
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
