package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGLSNAdapter(t *testing.T) {
	var d *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.PgLSN {
			d = candidate
		}
	}
	if d == nil {
		t.Fatal("missing pg_lsn descriptor")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		raw  any
		want driver.Value
	}{
		{"0/0", "0/0"}, {[]byte("a/b"), "A/B"}, {"80000000/0", "80000000/0"},
		{"ffffffff/ffffffff", "FFFFFFFF/FFFFFFFF"}, {nil, nil}, {[]byte(nil), nil},
	} {
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: tc.raw}})
		if err != nil {
			t.Fatal(err)
		}
		v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
		if err != nil || out != tc.want {
			t.Fatal(tc, out, err)
		}
	}
	if scanType(d) != reflect.TypeFor[string]() {
		t.Fatal(scanType(d))
	}
	mutable := []byte("0/1")
	args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: mutable}})
	if err != nil {
		t.Fatal(err)
	}
	mutable[2] = '9'
	v, err := importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exportValue(ctx, v, sqltype.OutputEnvironment{})
	if err != nil || out != "0/1" {
		t.Fatal("borrowed bytes escaped", out, err)
	}
	for _, raw := range []any{"bad", []byte("bad"), "", []byte{}} {
		args, err := normalizeArguments([]driver.NamedValue{{Ordinal: 1, Value: raw}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = importParameter(ctx, args[0], d.Usage(), sqltype.InputEnvironment{})
		var diagnostic *sqlstate.Error
		input := "bad"
		if s, ok := args[0].(string); ok {
			input = s
		}
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != "invalid input syntax for type pg_lsn: \""+input+"\"" || diagnostic.Context != "" || diagnostic.Position != "" {
			t.Fatal(raw, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := importParameter(canceled, "0/1", d.Usage(), sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := exportValue(canceled, v, sqltype.OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
