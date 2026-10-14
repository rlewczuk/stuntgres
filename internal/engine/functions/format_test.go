package functions

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func nullCountUnitDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

type formatWorkContext struct {
	context.Context
	checks int
}

func (c *formatWorkContext) Err() error {
	c.checks--
	if c.checks <= 0 {
		return context.Canceled
	}
	return nil
}
func TestFormatTextGrammarAndOutput(t *testing.T) {
	types := formatUnitTypes()
	text := func(s string) sqltype.Value { return sqltype.String(types[sqltype.Text], s) }
	integer := func(n int64) sqltype.Value {
		value, err := sqltype.Integer(types[sqltype.Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	padded, err := types[sqltype.BPChar].Input(context.Background(), "x", 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pattern, want string
		values        []sqltype.Value
	}{
		{"hello", "hello", nil}, {"", "", nil}, {"%%%%", "%%", nil},
		{"%3$s/%2$s/%s", "three/two/three", []sqltype.Value{text("one"), text("two"), text("three")}},
		{"%2$s/%s/%1$s/%s", "b/c/a/b", []sqltype.Value{text("a"), text("b"), text("c")}},
		{"%s%%/%s", "a%/b", []sqltype.Value{text("a"), text("b"), text("unused")}},
		{"|%05s|%-5s|%--5s|", "|    x|x    |x    |", []sqltype.Value{text("x"), text("x"), text("x")}},
		{"|%3s|%2s|", "|  é|é|", []sqltype.Value{text("é"), text("é")}},
		{"|%5I|%5L|", `|  "X"|  'x'|`, []sqltype.Value{text("X"), text("x")}},
		{"|%*s|", "|x    |", []sqltype.Value{integer(-5), text("x")}},
		{"|%*s|", "|    x|", []sqltype.Value{text(" +0b101 "), text("x")}},
		{"|%1$*2$s|%s", "|    x|5", []sqltype.Value{text("x"), integer(5), text("y")}},
		{"|%*2$s|", "|  bar|", []sqltype.Value{text("foo"), integer(5), text("bar")}},
		{"%2$*s/%s", "    x/y", []sqltype.Value{integer(5), text("x"), text("y")}},
		{"%I/%I/%I/%I", `foo/"select"/abort/"é"`, []sqltype.Value{text("foo"), text("select"), text("abort"), text("é")}},
		{"%I/%L", `"a""b"/'O''Reilly'`, []sqltype.Value{text(`a"b`), text("O'Reilly")}},
		{"%L", `E'a\\b'`, []sqltype.Value{text(`a\b`)}},
		{"%s/%I/%L", `x  /"x  "/'x  '`, []sqltype.Value{padded, padded, padded}},
		{"%s/%L", "t/'f'", []sqltype.Value{sqltype.Boolean(types[sqltype.Bool], true), sqltype.Boolean(types[sqltype.Bool], false)}},
		{"%s/%L", "/NULL", []sqltype.Value{sqltype.Null(types[sqltype.Text]), sqltype.Null(types[sqltype.Int4])}},
		{"|%*s|", "|x|", []sqltype.Value{sqltype.Null(types[sqltype.Int4]), text("x")}},
	} {
		t.Run(tc.pattern+tc.want, func(t *testing.T) {
			got, err := FormatText(context.Background(), tc.pattern, FormatValues{Ordinary: tc.values}, types[sqltype.Int4], sqltype.OutputEnvironment{})
			if err != nil || got != tc.want {
				t.Fatalf("%q: got %q/%v, want %q", tc.pattern, got, err, tc.want)
			}
		})
	}
}
func TestFormatTextDiagnosticsAndPrecedence(t *testing.T) {
	types := formatUnitTypes()
	text := func(s string) sqltype.Value { return sqltype.String(types[sqltype.Text], s) }
	minimum, err := sqltype.Integer(types[sqltype.Int4], -2147483648)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pattern, code, message, hint string
		values                       []sqltype.Value
	}{
		{"%", "22023", "unterminated format() type specifier", formatHint, nil},
		{"%1$", "22023", "unterminated format() type specifier", formatHint, nil},
		{"%*1$", "22023", "unterminated format() type specifier", formatHint, nil},
		{"%é", "22023", `unrecognized format() type specifier "é"`, formatHint, nil},
		{"%1-s", "22023", `unrecognized format() type specifier "-"`, formatHint, nil},
		{"%5%", "22023", `unrecognized format() type specifier "%"`, formatHint, nil},
		{"%*d", "22023", `unrecognized format() type specifier "d"`, formatHint, []sqltype.Value{text("bad")}},
		{"%0$s", "22023", "format specifies argument 0, but arguments are numbered from 1", "", nil},
		{"%*0$s", "22023", "format specifies argument 0, but arguments are numbered from 1", "", nil},
		{"%*1s", "22023", `width argument position must be ended by "$"`, "", nil},
		{"%s", "22023", "too few arguments for format()", "", nil},
		{"%*s", "22P02", `invalid input syntax for type integer: "bad"`, "", []sqltype.Value{text("bad")}},
		{"%2147483648", "22003", "number is out of range", "", nil},
		{"%*2147483648$s", "22003", "number is out of range", "", nil},
		{"%*s", "22003", "number is out of range", "", []sqltype.Value{minimum, text("x")}},
		{"%*I", "22004", "null values cannot be formatted as an SQL identifier", "", []sqltype.Value{minimum, sqltype.Null(types[sqltype.Text])}},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			_, err := FormatText(context.Background(), tc.pattern, FormatValues{Ordinary: tc.values}, types[sqltype.Int4], sqltype.OutputEnvironment{})
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message, Hint: tc.hint})
		})
	}
}
func TestFormatBoundedCancellationAndSizes(t *testing.T) {
	types := formatUnitTypes()
	for _, operation := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"parser", func(ctx context.Context) error {
			_, err := FormatText(ctx, strings.Repeat("a", 4096), FormatValues{}, types[sqltype.Int4], sqltype.OutputEnvironment{})
			return err
		}},
		{"digits", func(ctx context.Context) error {
			_, _, err := formatDigits(ctx, strings.Repeat("0", 4096), 0)
			return err
		}},
		{"copy", func(ctx context.Context) error {
			var out strings.Builder
			return formatWrite(ctx, &out, strings.Repeat("x", 16384))
		}},
		{"padding", func(ctx context.Context) error { var out strings.Builder; return formatPadding(ctx, &out, 16384) }},
		{"count", func(ctx context.Context) error {
			var out strings.Builder
			return formatField(ctx, &out, sqltype.String(types[sqltype.Text], strings.Repeat("é", 4096)), 's', 16384, false, sqltype.OutputEnvironment{})
		}},
		{"identifier", func(ctx context.Context) error {
			var out strings.Builder
			return formatField(ctx, &out, sqltype.String(types[sqltype.Text], strings.Repeat("a", 4096)), 'I', 0, false, sqltype.OutputEnvironment{})
		}},
		{"literal", func(ctx context.Context) error {
			var out strings.Builder
			return formatField(ctx, &out, sqltype.String(types[sqltype.Text], strings.Repeat("'", 4096)), 'L', 0, false, sqltype.OutputEnvironment{})
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			ctx := &formatWorkContext{Context: context.Background(), checks: 3}
			err := operation.run(ctx)
			var diagnostic *sqlstate.Error
			if !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
				t.Fatalf("ordinary bounded cancellation: %v", err)
			}
		})
	}
	_, err := formatSize(int(^uint(0)>>1), 1)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "54000", Message: "requested length too large"})
}
func TestFormatBorrowsExistingTypedArrayPayload(t *testing.T) {
	types := formatUnitTypes()
	arrayType := types[types[sqltype.Int4].AssociatedArray()]
	if arrayType == nil || !arrayType.IsArray() || arrayType.Element() != types[sqltype.Int4] {
		t.Fatal("owned upstream INT4 array descriptor")
	}
	seven, err := sqltype.Integer(types[sqltype.Int4], 7)
	if err != nil {
		t.Fatal(err)
	}
	four, err := sqltype.Integer(types[sqltype.Int4], 4)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: -1, Length: 2}}, []sqltype.Value{seven, four})
	if err != nil {
		t.Fatal(err)
	}
	borrowed := FormatValues{Array: value}
	if borrowed.Len() != 2 || borrowed.At(0).Type() != types[sqltype.Int4] || !sqltype.SameDatum(borrowed.At(0), seven) {
		t.Fatal("typed row-major payload identity")
	}
	got, err := FormatText(context.Background(), "|%1$*2$s|/%s", borrowed, types[sqltype.Int4], sqltype.OutputEnvironment{})
	if err != nil || got != "|   7|/4" {
		t.Fatal(got, err)
	}
	got, err = FormatText(context.Background(), "ok", FormatValues{Array: sqltype.Null(arrayType)}, types[sqltype.Int4], sqltype.OutputEnvironment{})
	if err != nil || got != "ok" {
		t.Fatal(got, err)
	}
	domain, err := sqltype.Domain(90002, "format_ints", "format_ints", arrayType.Usage())
	if err != nil {
		t.Fatal(err)
	}
	datum, err := domain.Input(context.Background(), "{7,4}", -1)
	if err != nil {
		t.Fatal(err)
	}
	got, err = FormatText(context.Background(), "%2$s/%1$s", FormatValues{Array: datum}, types[sqltype.Int4], sqltype.OutputEnvironment{})
	if err != nil || got != "4/7" {
		t.Fatal(got, err)
	}
	if value.ArrayDimensions()[0].Lower != -1 || value.ArrayElement(0).Type() != types[sqltype.Int4] {
		t.Fatal("format mutated borrowed array")
	}
}
