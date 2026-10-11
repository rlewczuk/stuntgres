package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntegerRangeAdapterOwnershipNullMetadata(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range} {
		d, ok := e.CatalogRead().LookupType(id)
		if !ok {
			t.Fatal("missing integer range descriptor")
		}
		bytes := []byte("[1,4]")
		arguments, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: bytes}})
		if err != nil {
			t.Fatal(err)
		}
		for i := range bytes {
			bytes[i] = 'x'
		}
		v, err := importParameter(ctx, arguments[0], d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || out != "[1,5)" || scanType(d) != reflect.TypeFor[string]() {
			t.Fatal(out, err, scanType(d))
		}
		for _, raw := range []driver.Value{nil, []byte(nil)} {
			v, err := importParameter(ctx, raw, d.Usage(), sqltype.InputEnvironment{})
			if err != nil || !v.IsNull() {
				t.Fatal(v, err)
			}
			out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
			if err != nil || out != nil {
				t.Fatal(out, err)
			}
		}
		for _, tc := range []struct{ input, output string }{{"empty", "empty"}, {"[,]", "(,)"}} {
			v, err := importParameter(ctx, tc.input, d.Usage(), sqltype.InputEnvironment{})
			if err != nil || v.IsNull() {
				t.Fatal(v, err)
			}
			out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
			if err != nil || out != tc.output {
				t.Fatal(out, err)
			}
		}
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: []byte{}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{}); err == nil {
			t.Fatal("empty bytes became NULL")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := importParameter(canceled, "[1,4]", d.Usage(), sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := exportValue(canceled, v, sqltype.OutputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestIntegerRangeAdapterPortalDiagnostics(t *testing.T) {
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range} {
		d, _ := e.CatalogRead().LookupType(id)
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{"", "22P02", `malformed range literal: ""`, "Missing left parenthesis or bracket.", `unnamed portal parameter $1 = ''`},
			{"[1,4", "22P02", `malformed range literal: "[1,4"`, "Unexpected end of input.", `unnamed portal parameter $1 = '...'`},
			{"[4,1)", "22000", "range lower bound must be less than or equal to range upper bound", "", `unnamed portal parameter $1 = '...'`},
		} {
			importer := frameImporter([]driver.Value{tc.input})
			err := importer(context.Background(), engine.ParameterInfo{Types: []sqltype.Usage{d.Usage()}}, make([]sqltype.Value, 1), sqltype.InputEnvironment{})
			want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Context: tc.context}
			var got *Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("got=%#v want=%#v err=%v", got, want, err)
			}
		}
	}
}
