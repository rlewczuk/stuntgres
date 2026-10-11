package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Each witness is independently specified before either backend is consulted.
func regexpScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func regexpRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	similarReferenceRoutes(t, p, b, s)
}

func regexpRows(b fixture.Backend, q string, names, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, rows, args...)
	s.Bag = !strings.Contains(strings.ToUpper(q), "ORDER BY")
	s.CheckMetadata = regexpMetadata(b.PG)
	return s
}

func regexpOne(b fixture.Backend, expr, typ string, value any, args ...any) fixture.Step {
	return regexpRows(b, "SELECT "+expr+" AS result", []string{"result"}, []string{typ}, [][]any{{value}}, args...)
}

func regexpMetadata(pg bool, nonNull ...string) func(*testing.T, *sql.Rows) {
	return regexpMetadataNullable(pg, nil, nonNull...)
}

func regexpMetadataNullable(pg bool, nullable []string, nonNull ...string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			var scan reflect.Type
			switch c.DatabaseTypeName() {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
				scan = reflect.TypeFor[string]()
			case "_TEXT":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatalf("unexpected regexp result type %q", c.DatabaseTypeName())
			}
			if c.ScanType() != scan {
				t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), scan)
			}
			knownNonNull := false
			for _, name := range nonNull {
				knownNonNull = knownNonNull || name == c.Name()
			}
			knownNullable := false
			for _, name := range nullable {
				knownNullable = knownNullable || name == c.Name()
			}
			wantNullable, wantKnown := !pg && knownNullable, !pg && (knownNullable || knownNonNull)
			if n, known := c.Nullable(); n != wantNullable || known != wantKnown {
				t.Fatalf("%s computed nullability (%t,%t), want (%t,%t)", c.Name(), n, known, wantNullable, wantKnown)
			}
			length, known := int64(0), false
			if c.DatabaseTypeName() == "TEXT" {
				length, known = math.MaxInt64, true
			}
			if got, ok := c.Length(); got != length || ok != known {
				t.Fatalf("%s Length=(%d,%t) want (%d,%t)", c.Name(), got, ok, length, known)
			}
			if p, s, ok := c.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatalf("%s unexpected DecimalSize", c.Name())
			}
		}
	}
}

func regexpBindError(q, code, msg, hint, token string) fixture.Step {
	pos := strings.Index(q, token)
	if pos < 0 {
		panic(fmt.Sprintf("missing diagnostic token %q", token))
	}
	return similarReferenceBindingError(q, fixture.Diagnostic{Code: code, Message: msg, Hint: hint, Position: fixture.Position(utf8.RuneCountInString(q[:pos]) + 1)})
}

func regexpRuntimeError(q, code, msg, hint string, args ...any) fixture.Step {
	return similarReferenceError(q, fixture.Diagnostic{Code: code, Message: msg, Hint: hint}, args...)
}
