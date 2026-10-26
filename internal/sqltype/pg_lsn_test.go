package sqltype

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func pgLSNUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}

func pgLSNUnitError(t *testing.T, err error, code, message, context string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing SQL diagnostic: %v", err)
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Context: context}
	if *diagnostic != want {
		t.Fatalf("diagnostic %#v; want %#v", diagnostic, want)
	}
}

func TestPGLSNCodec(t *testing.T) {
	types := testTypes()
	d, array := types[PgLSN], types[3221]
	if !d.Concrete() || d.ID() != 3220 || d.Name() != "pg_lsn" || d.SQLName() != "PG_LSN" || d.DatabaseName() != "PG_LSN" || d.AssociatedArray() != 3221 || array.Concrete() || array.Element() != d || !StoredScalarCastType(PgLSN) || ArrayElementAdmitted(PgLSN) {
		t.Fatal("scalar/companion authority", d, array)
	}
	if _, err := Array(90000, "_boundary", d); err == nil {
		t.Fatal("executable LSN array")
	}
	ctx := context.Background()
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL::pg_lsn" {
		t.Fatal(literal, err)
	}
	for _, tc := range []struct {
		input, output string
		bits          uint64
	}{
		{"0/0", "0/0", 0}, {"00000000/00000001", "0/1", 1}, {"a/b", "A/B", 0xA0000000B},
		{"16/b374d848", "16/B374D848", 0x16B374D848}, {"80000000/0", "80000000/0", 1 << 63},
		{"ffffffff/ffffffff", "FFFFFFFF/FFFFFFFF", ^uint64(0)},
	} {
		v := pgLSNUnitInput(t, d, tc.input)
		bits, err := v.PGLSN()
		if err != nil || bits != tc.bits {
			t.Fatal(bits, tc.bits, err)
		}
		constructed, err := PGLSNValue(d, tc.bits)
		if err != nil || !SameDatum(v, constructed) {
			t.Fatal("inline datum", err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc.output {
			t.Fatal(out, tc.output, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.output+"'::pg_lsn" {
			t.Fatal(literal, err)
		}
		binary, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
		if err != nil || len(binary) != 8 {
			t.Fatal(binary, err)
		}
		back, err := d.BinaryInputWithEnvironment(ctx, binary, -1, InputEnvironment{})
		if err != nil || !SameDatum(v, back) {
			t.Fatal("binary roundtrip", err)
		}
		if tc.bits == 0 && !bytes.Equal(binary, make([]byte, 8)) || tc.bits == ^uint64(0) && !bytes.Equal(binary, bytes.Repeat([]byte{255}, 8)) {
			t.Fatal(binary)
		}
	}
	for _, input := range []string{"", "bad", "/", "0/", "/0", "0//0", "0/0/0", "+0/0", "-1/0", "0x1/0", "G/0", "0/G", " 0/0", "0/0 ", "0 /0", "0/ 0", "0/\t0", "0/0\n", "000000000/0", "0/000000000", "100000000/0", "0/100000000", "0/0junk", "bad'\"\\\n"} {
		_, err := d.Input(ctx, input, -1)
		pgLSNUnitError(t, err, "22P02", "invalid input syntax for type pg_lsn: \""+input+"\"", "")
	}
	for _, size := range []int{0, 7, 9} {
		_, err := d.BinaryInputWithEnvironment(ctx, make([]byte, size), -1, InputEnvironment{})
		code, message := "08P01", "insufficient data left in message"
		if size == 9 {
			code, message = "22P03", "incorrect binary data format"
		}
		pgLSNUnitError(t, err, code, message, "")
	}
	low, _ := PGLSNValue(d, 0)
	high, _ := PGLSNValue(d, 1<<63)
	if SameDatum(low, high) {
		t.Fatal("high bits lost")
	}
	for _, wrong := range []*Descriptor{nil, types[Int8], types[Xid8]} {
		if _, err := PGLSNValue(wrong, 1); err == nil {
			t.Fatal("wrong constructor descriptor")
		}
	}
	for _, wrong := range []Value{{}, Null(d), {typ: types[Xid8]}} {
		if _, err := wrong.PGLSN(); err == nil {
			t.Fatal("wrong accessor descriptor or NULL")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, "0/0", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPGLSNComparison(t *testing.T) {
	d := testTypes()[PgLSN]
	p, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	values := []uint64{0, 1, 0xffffffff, 1 << 32, 1<<63 - 1, 1 << 63, ^uint64(0)}
	for i, x := range values {
		for j, y := range values {
			a, _ := PGLSNValue(d, x)
			b, _ := PGLSNValue(d, y)
			got, err := p.Compare(context.Background(), a, b, InputEnvironment{})
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if err != nil || got != want {
				t.Fatal(x, y, got, want, err)
			}
		}
	}
	class, ok := DefaultIndexClass(d)
	if !ok || class.OID() != 10067 || class.FamilyOID() != 3253 || class.InputOID() != 3220 || class.StorageOID() != 0 {
		t.Fatal(class, ok)
	}
	index, err := bindTestIndexOrdering(d.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	low, _ := PGLSNValue(d, 0)
	high, _ := PGLSNValue(d, ^uint64(0))
	if got, err := index.Compare(context.Background(), low, high, InputEnvironment{}); err != nil || got != -1 {
		t.Fatal("unsigned index comparator", got, err)
	}
	if hash, _ := LookupHashIndexClass("pg_lsn_ops"); hash.Accepts(d) {
		t.Fatal("hash provider invented", hash)
	}
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Length != 8 || !facts.ByValue || facts.Alignment != "d" || facts.Category != "U" || facts.Preferred || facts.Input != 3229 || facts.Output != 3230 || facts.Receive != 3238 || facts.Send != 3239 {
		t.Fatal(facts, ok)
	}
}
