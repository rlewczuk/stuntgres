package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMultirangeAdapterOwnershipNullMetadata(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange} {
		d, ok := e.CatalogRead().LookupType(id)
		if !ok {
			t.Fatal("missing companion descriptor", id)
		}
		owned := []byte("{[1,3),[2,4),empty,[8,9)}")
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: owned}})
		if err != nil {
			t.Fatal(err)
		}
		for i := range owned {
			owned[i] = 'x'
		}
		v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || out != "{[1,4),[8,9)}" || scanType(d) != reflect.TypeFor[string]() {
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
		for _, raw := range []string{"{}", "{(,)}"} {
			v, err := importParameter(ctx, raw, d.Usage(), sqltype.InputEnvironment{})
			if err != nil || v.IsNull() {
				t.Fatal(v, err)
			}
			out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
			if err != nil || out != raw {
				t.Fatal(out, err)
			}
		}
		args, err = normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: []byte{}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{}); err == nil {
			t.Fatal("empty bytes became NULL")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := importParameter(canceled, "{}", d.Usage(), sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := exportValue(canceled, v, sqltype.OutputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestMultirangeAdapterPortalDiagnostics(t *testing.T) {
	e := engine.New()
	for _, id := range []sqltype.ID{sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange} {
		d, _ := e.CatalogRead().LookupType(id)
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{"", "22P02", `malformed multirange literal: ""`, "Missing left brace.", `unnamed portal parameter $1 = ''`},
			{"{[1,4", "22P02", `malformed multirange literal: "{[1,4"`, "Unexpected end of input.", `unnamed portal parameter $1 = '...'`},
			{"{[4,1)}", "22000", "range lower bound must be less than or equal to range upper bound", "", `unnamed portal parameter $1 = '...'`},
		} {
			importer := frameImporter([]driver.Value{tc.input})
			err := importer(context.Background(), analyze.ParameterInfo{Types: []sqltype.Usage{d.Usage()}}, make([]sqltype.Value, 1), sqltype.InputEnvironment{})
			want := Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Context: tc.context}
			var got *Error
			if !errors.As(err, &got) || *got != want {
				t.Fatalf("got=%#v want=%#v err=%v", got, want, err)
			}
		}
	}
}
