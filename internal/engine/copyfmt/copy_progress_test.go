package copyfmt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestCopyProgressWriterBoundaries(t *testing.T) {
	text := textDescriptor(t)
	schema := []catalog.Column{{Name: "v", Usage: text.Usage()}}
	for _, tc := range []struct {
		name    string
		options Options
		ends    []int
		total   int
	}{
		{"text_header", Options{Format: Text, Delimiter: '\t', Null: `\N`, Header: 1}, []int{4, 6}, 6},
		{"csv_header", Options{Format: CSV, Delimiter: ',', Quote: '"', Escape: '"', Header: 1}, []int{4, 6}, 6},
		{"binary", Options{Format: Binary}, []int{26, 33}, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := func() *testRows {
				return &testRows{rows: [][]sqltype.Value{{sqltype.String(text, "x")}, {sqltype.String(text, "y")}}}
			}
			var output bytes.Buffer
			var ends []int
			count, err := WriteRows(context.Background(), &output, source(), schema, tc.options, true, func() { ends = append(ends, output.Len()) })
			if err != nil || count != 2 || output.Len() != tc.total || !reflect.DeepEqual(ends, tc.ends) {
				t.Fatalf("count=%d err=%v bytes=%d callbacks=%v", count, err, output.Len(), ends)
			}
			for _, partialError := range []bool{false, true} {
				failure := errors.New("partial COPY output")
				writer := &progressPartialWriter{failAt: 2, err: failure, withError: partialError}
				calls := 0
				count, err = WriteRows(context.Background(), writer, source(), schema, tc.options, true, func() { calls++ })
				// Each case writes framing/header first; the first tuple fails partway.
				want := io.ErrShortWrite
				if partialError {
					want = failure
				}
				if !errors.Is(err, want) || count != 0 || calls != 0 || writer.Len() == 0 {
					t.Fatalf("failed row reported completion count=%d callbacks=%d err=%v", count, calls, err)
				}
			}
		})
	}
}

type progressPartialWriter struct {
	bytes.Buffer
	calls, failAt int
	err           error
	withError     bool
}

func (w *progressPartialWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls != w.failAt {
		return w.Buffer.Write(p)
	}
	n := len(p) - 1
	w.Buffer.Write(p[:n])
	if w.withError {
		return n, w.err
	}
	return n, nil
}
