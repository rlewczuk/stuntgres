package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func timeoutOracleFrame(stream byte, payload string) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

type timeoutOracleCloseFailure struct {
	io.Reader
	err error
}

func (r timeoutOracleCloseFailure) Close() error { return r.err }

func TestReferenceTimeoutOracleCaptureDataAndErrors(t *testing.T) {
	first := timeoutOracleFrame(1, "before\n")
	second := timeoutOracleFrame(2, "timeout-oracle|42|57P05|FATAL:  terminating connection due to idle-session timeout\n")
	all := append(append([]byte{}, first...), second...)
	want := "before\ntimeout-oracle|42|57P05|FATAL:  terminating connection due to idle-session timeout\n"
	t.Run("ordered_streams_exact_cap", func(t *testing.T) {
		got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(all)), len(all))
		if err != nil || got != want {
			t.Fatalf("capture=%q/%v want %q", got, err, want)
		}
	})
	t.Run("empty_frames", func(t *testing.T) {
		input := append(timeoutOracleFrame(1, ""), timeoutOracleFrame(2, "x")...)
		got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(input)), len(input))
		if err != nil || got != "x" {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
	t.Run("cumulative_cap", func(t *testing.T) {
		got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(all)), len(all)-1)
		if got != "" || err == nil || !strings.Contains(err.Error(), "exceeds bounded capture") {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
	t.Run("oversized_header_before_payload", func(t *testing.T) {
		header := make([]byte, 8)
		header[0] = 2
		binary.BigEndian.PutUint32(header[4:], math.MaxUint32)
		got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(header)), 16)
		if got != "" || err == nil || !strings.Contains(err.Error(), "exceeds bounded capture") {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{"truncated_header", first[:7]},
		{"truncated_payload", first[:len(first)-1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(tc.input)), 128)
			if got != "" || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("capture=%q/%v", got, err)
			}
		})
	}
	t.Run("unknown_stream", func(t *testing.T) {
		got, err := timeoutReadOracleLogs(context.Background(), io.NopCloser(bytes.NewReader(timeoutOracleFrame(3, "daemon failure"))), 128)
		if got != "" || err == nil || !strings.Contains(err.Error(), "unknown Docker log stream: 3") {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
	t.Run("read_failure", func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		injected := errors.New("log transport failure")
		writer.CloseWithError(injected)
		got, err := timeoutReadOracleLogs(context.Background(), reader, 128)
		if got != "" || !errors.Is(err, injected) {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
	t.Run("close_failure_rejects_evidence", func(t *testing.T) {
		injected := errors.New("log response close failure")
		got, err := timeoutReadOracleLogs(context.Background(), timeoutOracleCloseFailure{bytes.NewReader(first), injected}, 128)
		if got != "" || !errors.Is(err, injected) {
			t.Fatalf("capture=%q/%v", got, err)
		}
	})
}

func TestReferenceTimeoutOracleCaptureCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	type captureResult struct {
		data string
		err  error
	}
	result := make(chan captureResult, 1)
	go func() { got, err := timeoutReadOracleLogs(ctx, reader, 128); result <- captureResult{got, err} }()
	// Actual pipe consumption establishes the read before cancellation, without
	// sleeps or goroutine/count assertions. The advertised payload never arrives.
	header := timeoutOracleFrame(2, "x")[:8]
	if _, err := writer.Write(header); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case captured := <-result:
		if captured.data != "" || !errors.Is(captured.err, context.Canceled) {
			t.Fatalf("capture=%q/%v", captured.data, captured.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled raw log read did not return")
	}
	if _, err := writer.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("canceled response still writable: %v", err)
	}
}
