package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

type timeoutOracleFrameReader struct {
	io.Reader
	closes   int
	closeErr error
}

func (r *timeoutOracleFrameReader) Close() error {
	r.closes++
	return r.closeErr
}

func TestTimeoutOracleLogFrames(t *testing.T) {
	oversized := timeoutOracleFrame(2, "")
	// The cap includes the wire header, not just its decoded payload.
	binary.BigEndian.PutUint32(oversized[4:8], (32<<20)-7)
	reserved := timeoutOracleFrame(2, "fatal\n")
	reserved[1] = 1
	for _, tc := range []struct {
		name string
		data []byte
		want string
		fail bool
	}{
		{"empty", nil, "", false},
		{"stdout_stderr", append(timeoutOracleFrame(1, "notice\n"), timeoutOracleFrame(2, "fatal\n")...), "notice\nfatal\n", false},
		{"empty_frame", timeoutOracleFrame(2, ""), "", false},
		{"partial_body", timeoutOracleFrame(2, "fatal\n")[:10], "", true},
		{"missing_body", timeoutOracleFrame(2, "fatal\n")[:8], "", true},
		{"daemon_error", timeoutOracleFrame(3, "daemon failure"), "", true},
		{"invalid_stream", timeoutOracleFrame(0, ""), "", true},
		{"reserved_header", reserved, "", true},
		{"wire_cap", oversized, "", true},
		{"partial_next_header", append(timeoutOracleFrame(2, "fatal\n"), 2), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &timeoutOracleFrameReader{Reader: bytes.NewReader(tc.data)}
			got, err := timeoutReadOracleLogs(context.Background(), reader, 32<<20)
			if (err != nil) != tc.fail || got != tc.want || reader.closes != 1 {
				t.Fatalf("text=%q error=%v closes=%d; want text=%q failure=%v one close", got, err, reader.closes, tc.want, tc.fail)
			}
		})
	}
	for n := range 7 {
		reader := &timeoutOracleFrameReader{Reader: bytes.NewReader(timeoutOracleFrame(2, "")[:n+1])}
		if _, err := timeoutReadOracleLogs(context.Background(), reader, 32<<20); err == nil || reader.closes != 1 {
			t.Fatalf("partial header bytes=%d error=%v closes=%d", n+1, err, reader.closes)
		}
	}
	t.Run("cumulative_wire_cap", func(t *testing.T) {
		header := timeoutOracleFrame(2, "")
		// Fits an empty capture, but not after the first complete wire frame.
		binary.BigEndian.PutUint32(header[4:8], (32<<20)-16)
		reader := &timeoutOracleFrameReader{Reader: bytes.NewReader(append(timeoutOracleFrame(1, "notice\n"), header...))}
		if _, err := timeoutReadOracleLogs(context.Background(), reader, 32<<20); err == nil || !strings.Contains(err.Error(), "exceeds bounded capture") || reader.closes != 1 {
			t.Fatalf("cumulative cap error=%v closes=%d", err, reader.closes)
		}
	})
	closeFailure := errors.New("raw log close failed")
	t.Run("close_error", func(t *testing.T) {
		reader := &timeoutOracleFrameReader{Reader: bytes.NewReader(nil), closeErr: closeFailure}
		if _, err := timeoutReadOracleLogs(context.Background(), reader, 32<<20); !errors.Is(err, closeFailure) || reader.closes != 1 {
			t.Fatalf("close error=%v closes=%d", err, reader.closes)
		}
	})
	t.Run("read_and_close_errors", func(t *testing.T) {
		reader := &timeoutOracleFrameReader{Reader: iotest.ErrReader(io.ErrClosedPipe), closeErr: closeFailure}
		if _, err := timeoutReadOracleLogs(context.Background(), reader, 32<<20); !errors.Is(err, io.ErrClosedPipe) || !errors.Is(err, closeFailure) || reader.closes != 1 {
			t.Fatalf("read/close errors=%v closes=%d", err, reader.closes)
		}
	})
}
