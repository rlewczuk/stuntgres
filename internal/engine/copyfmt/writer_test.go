package copyfmt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type testRows struct{ rows [][]sqltype.Value }

func (r *testRows) Next(_ context.Context, values []sqltype.Value) (bool, error) {
	if len(r.rows) == 0 {
		return false, nil
	}
	copy(values, r.rows[0])
	r.rows = r.rows[1:]
	return true, nil
}
func (r *testRows) OutputEnvironment() sqltype.OutputEnvironment { return sqltype.OutputEnvironment{} }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func textDescriptor(t *testing.T) *sqltype.Descriptor {
	for _, d := range sqltype.Builtins() {
		if d.Name() == "text" {
			return d
		}
	}
	t.Fatal("no text type")
	return nil
}

func TestCopyWriteRows(t *testing.T) {
	text := textDescriptor(t)
	schema := []catalog.Column{{Name: "a", Usage: sqltype.Usage{Type: text}}, {Name: "b,c", Usage: sqltype.Usage{Type: text}}}
	source := func() *testRows {
		return &testRows{rows: [][]sqltype.Value{
			{sqltype.String(text, "x\ty"), sqltype.Null(text)},
			{sqltype.String(text, "p,q"), sqltype.String(text, "")},
		}}
	}
	binaryWant := append([]byte(nil), BinarySignature...)
	binaryWant = append(binaryWant, 0, 0, 0, 0, 0, 0, 0, 0)
	binaryWant = append(binaryWant, 0, 2, 0, 0, 0, 3, 'x', '\t', 'y', 0xff, 0xff, 0xff, 0xff)
	binaryWant = append(binaryWant, 0, 2, 0, 0, 0, 3, 'p', ',', 'q', 0, 0, 0, 0)
	binaryWant = append(binaryWant, 0xff, 0xff)
	for _, tc := range []struct {
		name string
		o    Options
		want string
	}{
		{"text", Options{Format: Text, Delimiter: '\t', Null: `\N`, Header: 1}, "a\tb,c\nx\\ty\t\\N\np,q\t\n"},
		{"csv", Options{Format: CSV, Delimiter: ',', Quote: '"', Escape: '"', Header: 1, QuoteFields: []bool{false, true}}, "a,\"b,c\"\nx\ty,\n\"p,q\",\"\"\n"},
		{"binary", Options{Format: Binary}, string(binaryWant)},
	} {
		var out bytes.Buffer
		count, err := WriteRows(context.Background(), &out, source(), schema, tc.o, true, nil)
		if err != nil || count != 2 || out.String() != tc.want {
			t.Fatalf("%s: count=%d err=%v\n got %q\nwant %q", tc.name, count, err, out.String(), tc.want)
		}
		count, err = WriteRows(context.Background(), shortWriter{}, source(), schema, tc.o, false, nil)
		if count != 0 || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("%s short write: count=%d err=%v", tc.name, count, err)
		}
	}
}
