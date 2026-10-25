package enginetests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func hstoreUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	d, err := sqltype.Hstore(id, "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHstoreCodec(t *testing.T) {
	ctx := context.Background()
	d := hstoreUnitType(t, 65000)
	for _, tc := range []struct {
		id                sqltype.ID
		name, declaration string
	}{
		{0, "hstore", "public.hstore"}, {sqltype.Unknown, "hstore", "public.hstore"},
		{65000, "", "public.hstore"}, {65000, "hstore", ""},
	} {
		if d, err := sqltype.Hstore(tc.id, tc.name, tc.declaration); err == nil || d != nil {
			t.Fatal("invalid scalar declaration admitted", tc, d, err)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {" \t\n\r\v\f", ""},
		{"aa=>long,b=>short,a=>NULL", `"a"=>NULL, "b"=>"short", "aa"=>"long"`},
		{"a=>NuLl", `"a"=>NULL`}, {`a=>"NULL"`, `"a"=>"NULL"`},
		{`N\ULL=>N\ULL`, `"NULL"=>NULL`}, {`a=>"N\ULL"`, `"a"=>"NULL"`},
		{`""=>""`, `""=>""`}, {"a=>b, \t", `"a"=>"b"`},
		{"a=>q=w", `"a"=>"q=w"`}, {"a=>q>w", `"a"=>"q>w"`},
		{`a=>b\qc`, `"a"=>"bqc"`}, {`"a\"b"=>"c\\d"`, `"a\"b"=>"c\\d"`},
		{"é=>雪,aa=>z,b=>β", `"b"=>"β", "aa"=>"z", "é"=>"雪"`},
		{"\u00a0a=>b", "\"\u00a0a\"=>\"b\""},
		{"a,b=>c", `"a,b"=>"c"`}, {"a=>,b", `"a"=>",b"`},
		{`a=>b"c`, `"a"=>"b\"c"`}, {"a=>1,a=>1", `"a"=>"1"`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil || v.IsNull() || v.Type() != d || !v.ValidResult(d.Usage()) {
				t.Fatalf("input %#v: %v/%v", tc.input, v, err)
			}
			text, err := d.Output(ctx, v)
			if err != nil || text != tc.want {
				t.Fatalf("output %q/%v want %q", text, err, tc.want)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+strings.ReplaceAll(tc.want, "'", "''")+"'" {
				t.Fatalf("SQL literal %q/%v", literal, err)
			}
			parsed, err := d.Input(ctx, tc.want, -1)
			if err != nil || !sqltype.SameDatum(v, parsed) {
				t.Fatalf("canonical reinput lost representation: %v", err)
			}
			if same, err := v.SamePayload(ctx, parsed); err != nil || !same {
				t.Fatalf("independent canonical payload %v/%v", same, err)
			}
		})
	}
	for _, tc := range []struct{ input, code, message string }{
		{"a=b", "42601", `syntax error in hstore, near "b" at position 2`},
		{" =>b", "42601", `syntax error in hstore, near "=" at position 1`},
		{"é=雪", "42601", `syntax error in hstore, near "雪" at position 3`},
		{"a=>", "42601", "syntax error in hstore: unexpected end of string"},
		{`a=>b\`, "42601", "syntax error in hstore: unexpected end of string"},
		{`"a`, "42601", "syntax error in hstore: unexpected end of string"},
		{`a=>"b`, "42601", "syntax error in hstore: unexpected end of string"},
		{"a=>\x00", "22021", `invalid byte sequence for encoding "UTF8": 0x00`},
		{"a=>\xff", "22021", `invalid byte sequence for encoding "UTF8": 0xff`},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		var diagnostic *sqlstate.Error
		if v.Type() != nil || !errors.As(err, &diagnostic) {
			t.Fatalf("failed input %q returned %v/%v", tc.input, v, err)
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if diagnostic.Severity != want.Severity || diagnostic.SeverityUnlocalized != want.SeverityUnlocalized || diagnostic.Code != want.Code || diagnostic.Message != want.Message || diagnostic.Detail != "" || diagnostic.Hint != "" || diagnostic.Position != "" || diagnostic.Context != "" {
			t.Fatalf("codec diagnostic %#v want %#v", diagnostic, want)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(ctx, "a=>b", -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled input: %v", err)
	}
	t.Run("reached_cancellation", func(t *testing.T) {
		text := "a=>" + strings.Repeat(`\x`, 4096)
		value, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []bool{false, true} {
			base, cancel := context.WithCancel(context.Background())
			ctx := &hstoreCancelContext{Context: base, cancel: cancel, remaining: 8}
			if output {
				got, err := d.Output(ctx, value)
				if !errors.Is(err, context.Canceled) || got != "" {
					t.Fatalf("reached output cancellation %q/%v", got, err)
				}
			} else {
				got, err := d.Input(ctx, text, -1)
				if !errors.Is(err, context.Canceled) || got.Type() != nil {
					t.Fatalf("reached input cancellation %v/%v", got, err)
				}
			}
			cancel()
		}
	})
}

// Trigger cancellation from a reached codec checkpoint, not a timer or race.
type hstoreCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *hstoreCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestHstoreRepresentation(t *testing.T) {
	d := hstoreUnitType(t, 65000)
	ctx := context.Background()
	inputs := []string{"", "a=>NULL", `a=>"NULL"`, "a=>1"}
	var values []sqltype.Value
	for _, text := range inputs {
		v, err := d.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	values = append(values, sqltype.Null(d))
	for i, a := range values {
		for j, b := range values {
			if sqltype.SameDatum(a, b) != (i == j) {
				t.Fatalf("representation distinctions collapsed %d/%d", i, j)
			}
		}
	}
	duplicate, err := d.Input(ctx, "a=>1,a=>2", -1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := d.Output(ctx, duplicate)
	if err != nil || text != `"a"=>"1"` && text != `"a"=>"2"` {
		t.Fatalf("duplicate uniqueness %q/%v", text, err)
	}
	if !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "HSTORE" || d.PostgreSQLTypeLength() != -1 {
		t.Fatal("scalar identity facts")
	}
	if _, err := sqltype.Array(65001, "_hstore", d); err == nil {
		t.Fatal("scalar introduction admitted executable arrays")
	}
	var zero sqltype.Value
	if zero.ValidResult(d.Usage()) {
		t.Fatal("missing payload admitted")
	}
	if _, err := zero.Retag(d); err == nil {
		t.Fatal("zero datum retagged")
	}
	if _, err := zero.RemapHstore(d); err == nil {
		t.Fatal("zero datum remapped")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Output(ctx, values[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty output: %v", err)
	}
}

func TestHstoreRemap(t *testing.T) {
	ctx := context.Background()
	source, sameID, destination := hstoreUnitType(t, 65000), hstoreUnitType(t, 65000), hstoreUnitType(t, 65001)
	for _, input := range []string{"", "a=>NULL", `a=>"NULL"`, `"a\"b"=>"c\\d"`} {
		original, err := source.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		want, err := source.Output(ctx, original)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []*sqltype.Descriptor{sameID, destination} {
			if _, err := original.Retag(target); err == nil {
				t.Fatal("foreign generation retagged")
			}
			mapped, err := original.RemapHstore(target)
			if err != nil || mapped.Type() != target {
				t.Fatalf("authorized remap: %v", err)
			}
			if same, err := original.SamePayload(ctx, mapped); err != nil || !same {
				t.Fatalf("remap changed representation %v/%v", same, err)
			}
			got, err := target.Output(ctx, mapped)
			if err != nil || got != want {
				t.Fatalf("remap changed contents %q/%v want %q", got, err, want)
			}
			got, err = source.Output(ctx, original)
			if err != nil || got != want {
				t.Fatal("source changed", got, err)
			}
		}
	}
	mapped, err := sqltype.Null(source).RemapHstore(destination)
	if err != nil || !mapped.IsNull() || mapped.Type() != destination {
		t.Fatal("typed NULL remap", err)
	}
	if _, err := sqltype.Null(source).Retag(sameID); err == nil {
		t.Fatal("foreign NULL generation retagged")
	}
	foreign, err := sqltype.QueryInt(source.ID(), "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqltype.Null(source).RemapHstore(foreign); err == nil {
		t.Fatal("foreign destination family accepted")
	}
	if _, err := sqltype.Null(foreign).RemapHstore(destination); err == nil {
		t.Fatal("foreign source family accepted")
	}
}
