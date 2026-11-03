package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations in this family come from PG18 documentation and REL_18_6
// tsrank.c/wparser_def.c, never from the native implementation. Approximate
// regression literals explicitly carry their released decimal tolerance.
type ftsRankWant struct {
	value     float64
	tolerance float64
	bits      *uint32
}

func ftsRankExact(value float64) ftsRankWant {
	return ftsRankWant{value: float64(float32(value)), tolerance: math.Abs(value) * 2.5e-7}
}

func ftsRankBits(bits uint32) ftsRankWant {
	return ftsRankWant{value: float64(math.Float32frombits(bits)), bits: &bits}
}

// Only original stored-view projections opt into native declaration metadata.
// Their raw/value expectations remain identical; pq keeps nullability unknown.
type ftsRankViewWant struct{ value any }

func ftsRankColumn(t *testing.T, c *sql.ColumnType, pg bool, label, typ string, knownNullable bool) {
	t.Helper()
	scan := reflect.TypeFor[string]()
	if typ == "FLOAT4" {
		scan = reflect.TypeFor[float64]()
		if pg {
			scan = reflect.TypeFor[float32]()
		}
	}
	if c.Name() != label || c.DatabaseTypeName() != typ || c.ScanType() != scan {
		t.Fatalf("column name=%q type=%q scan=%v; want %q %s %v", c.Name(), c.DatabaseTypeName(), c.ScanType(), label, typ, scan)
	}
	if value, known := c.Nullable(); value != knownNullable || known != knownNullable {
		t.Fatal("Nullable", value, known)
	}
	length, lengthKnown := int64(0), false
	if typ == "TEXT" {
		length, lengthKnown = math.MaxInt64, true
	}
	if value, known := c.Length(); value != length || known != lengthKnown {
		t.Fatal("Length", value, known)
	}
	if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("DecimalSize", precision, scale, known)
	}
}

// All three routes consume raw public driver values, retain descriptors through
// EOF/Close, and assert independently before any fixture stream comparison.
func ftsRankRead(t *testing.T, p *fixture.Pair, b fixture.Backend, rows *sql.Rows, label, typ string, want any, zero bool) any {
	t.Helper()
	knownNullable := false
	if view, ok := want.(ftsRankViewWant); ok {
		want = view.value
		knownNullable = !b.PG
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 {
		t.Fatal("column count", len(columns))
	}
	ftsRankColumn(t, columns[0], b.PG, label, typ, knownNullable)
	var retained any
	if rows.Next() {
		if zero {
			t.Fatal("zero-row query published a row")
		}
		if err := rows.Scan(&retained); err != nil {
			t.Fatal(err)
		}
		switch expected := want.(type) {
		case ftsRankWant:
			value, ok := retained.(float64)
			if !ok {
				t.Fatalf("raw REAL = %T(%v), want float64", retained, retained)
			}
			// lib/pq parses PostgreSQL's shortest REAL text into float64; only
			// native values are exact binary32 widenings before this boundary.
			if !b.PG && value != float64(float32(value)) {
				t.Fatalf("REAL is not binary32: %.18g", value)
			}
			if expected.bits != nil {
				if got := math.Float32bits(float32(value)); got != *expected.bits {
					t.Fatalf("REAL bits=%08x want=%08x (value %.18g)", got, *expected.bits, value)
				}
			} else if math.IsNaN(value) || math.Abs(value-expected.value) > expected.tolerance {
				t.Fatalf("REAL %.18g want %.18g +/- %.3g", value, expected.value, expected.tolerance)
			}
		default:
			if !reflect.DeepEqual(retained, want) {
				t.Fatalf("raw %s = %T(%v), want %T(%v)", typ, retained, retained, want, want)
			}
		}
		if rows.Next() {
			t.Fatal("extra result row")
		}
	} else if !zero {
		t.Fatalf("missing result row: %v", rows.Err())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	ftsRankColumn(t, columns[0], b.PG, label, typ, knownNullable)
	return retained
}

func ftsRankRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query, label, typ string, want any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
			if !prepared {
				rows, err := c.QueryContext(p.Ctx, query, args...)
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, label, typ, want, false)
				return
			}
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.Ctx, args...)
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, label, typ, want, false)
		})
	}
}

func ftsRankDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic, analyze bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, selectRows := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, want, args...)
			s.Prepared, s.SelectRows, s.ErrorStage = prepared, selectRows, "execute"
			if prepared && analyze {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
	if !analyze {
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("runtime diagnostic failed during Prepare", err)
		}
		defer stmt.Close()
		for range 2 {
			for _, selectRows := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(query, want, args...)
				s.SelectRows, s.ErrorStage = selectRows, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
}

func ftsRankMissing(signature string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	d.Position = fixture.Position(position)
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return d
}

var ftsRankNames = []string{"ts_rank", "ts_rank_cd"}

// The argument order is the released four real wrappers, not SQL defaults.
var ftsRankForms = [][]string{
	{"tsvector", "tsquery"}, {"tsvector", "tsquery", "integer"},
	{"real[]", "tsvector", "tsquery"}, {"real[]", "tsvector", "tsquery", "integer"},
}

func ftsRankParameters(types []string) string {
	query := ""
	for i, typ := range types {
		if i != 0 {
			query += ","
		}
		query += fmt.Sprintf("$%d::%s", i+1, typ)
	}
	return query
}

func ftsRankArguments(types []string, vector, query string) []any {
	args := make([]any, len(types))
	for i, typ := range types {
		switch typ {
		case "tsvector":
			args[i] = vector
		case "tsquery":
			args[i] = query
		case "real[]":
			args[i] = "{0.1,0.2,0.4,1}"
		case "integer":
			args[i] = int64(0)
		}
	}
	return args
}

// REL_18_6 parse_func.c rejects aggregate/window decoration only after
// ordinary signature resolution. WITHIN GROUP's ordering input participates
// in that resolution, hence the explicitly compatible third argument.
func ftsRankDecorations(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, left, leftType, ordering string) {
	t.Helper()
	arguments := left + ",'cat'::tsquery"
	for _, tc := range []struct{ expression, message string }{
		{name + "(DISTINCT " + arguments + ")", "DISTINCT specified, but " + name + " is not an aggregate function"},
		{name + "(" + arguments + " ORDER BY " + left + ")", "ORDER BY specified, but " + name + " is not an aggregate function"},
		{name + "(" + arguments + ") WITHIN GROUP (ORDER BY " + ordering + ")", "WITHIN GROUP specified, but " + name + " is not an aggregate function"},
		{name + "(" + arguments + ") FILTER (WHERE true)", "FILTER specified, but " + name + " is not an aggregate function"},
		{name + "(" + arguments + ") OVER ()", "OVER specified, but " + name + " is not a window function nor an aggregate function"},
	} {
		d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
		d.Position = "8"
		ftsRankDiagnosticRoutes(t, p, b, c, "SELECT "+tc.expression, d, true)
	}
	ftsRankDiagnosticRoutes(t, p, b, c, "SELECT "+name+"("+left+",VARIADIC ARRAY['cat'::tsquery])", ftsRankMissing(name+"("+leftType+", tsquery[])", 8), true)
}

func ftsRankEmptyQueryNotices(t *testing.T, p *fixture.Pair, b fixture.Backend, name string) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	expression, typ := name+"('cat:1'::tsvector,$1::tsquery)", "FLOAT4"
	var want any = float64(0)
	if name == "ts_headline" {
		expression, typ, want = "ts_headline('english'::regconfig,'Cat',$1::tsquery)", "TEXT", "Cat"
	}
	query := "SELECT " + expression + " AS result"
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if len(notices) != 0 {
		t.Fatal("Prepare emitted empty-query notice", notices)
	}
	expected := []fixture.Diagnostic{ftsTypesEmptyNotice("", "", "unnamed portal parameter $1 = ''")}
	for _, route := range []string{"direct", "fresh", "held", "held"} {
		s := fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{want}}, "")
		if route == "held" {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, recorder, s)
		} else {
			s.Prepared = route == "fresh"
			fixture.RunStep(t, p.Ctx, c, recorder, s)
		}
		if !reflect.DeepEqual(notices, expected) {
			t.Fatalf("%s empty-query notices=%#v want=%#v", route, notices, expected)
		}
		notices = nil
	}
}
