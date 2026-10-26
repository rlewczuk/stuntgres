package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func routineScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func routineExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func routineValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name, typ string, value any, args ...any) {
	t.Helper()
	routineValueWithNativeNullability(t, p, b, c, q, name, typ, value, false, false, args...)
}

func routineValueWithNativeNullability(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, name, typ string, value any, nativeNullable, nativeNullableKnown bool, args ...any) {
	t.Helper()
	s := routineSelect(b,q,name,typ,value,args...)
	s.CheckMetadata = routineScalarMetadata(b, name, typ, nativeNullable, nativeNullableKnown)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func routineTypeName(b fixture.Backend, typ string) string {
	if b.PG {
		switch typ {
		case "INT4MULTIRANGE", "ANYMULTIRANGE", "ANYCOMPATIBLEARRAY", "ANYCOMPATIBLERANGE", "ANYCOMPATIBLEMULTIRANGE":
			return ""
		}
	}
	return typ
}

func routineSelect(b fixture.Backend, query, name, typ string, value any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{name}, []string{routineTypeName(b,typ)}, [][]any{{value}}, args...)
	s.RawTypes = []string{typ}
	if typ == "" {
		// User enums have no client database type name on either backend.
		s.RawTypes[0] = "_ENUM"
	}
	return s
}

func routineScalarMetadata(b fixture.Backend, name, typ string, nativeNullable, nativeNullableKnown bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(columns) != 1 {
			t.Fatal("routine column count", len(columns))
		}
		col := columns[0]
		if col.Name() != name || col.DatabaseTypeName() != routineTypeName(b,typ) {
			t.Fatal("routine declared metadata", col)
		}
		switch typ {
		case "TEXT", "NUMERIC", "RECORD", "VOID", "CSTRING", "_INT4", "_NUMERIC", "INT4RANGE", "INT4MULTIRANGE", "", "ANYARRAY", "ANYRANGE", "ANYMULTIRANGE", "ANYCOMPATIBLEARRAY", "ANYCOMPATIBLERANGE", "ANYCOMPATIBLEMULTIRANGE", "PG_DDL_COMMAND":
			routineExtendedMetadata(t, b, col, typ, nativeNullable, nativeNullableKnown)
			return
		}
		if b.PG {
			// Independently observed on pinned18.6 in tests-1.md/tests-2.md.
			var wantScan reflect.Type
			switch typ {
			case "INT4":
				wantScan = reflect.TypeFor[int32]()
			case "INT8":
				wantScan = reflect.TypeFor[int64]()
			case "FLOAT8":
				wantScan = reflect.TypeFor[float64]()
			case "BOOL":
				wantScan = reflect.TypeFor[bool]()
			default:
				t.Fatalf("missing routine metadata expectation for %s", typ)
			}
			_, lengthOK := col.Length()
			_, _, decimalOK := col.DecimalSize()
			nullable, nullableOK := col.Nullable()
			if col.ScanType() != wantScan || lengthOK || decimalOK || nullable || nullableOK {
				t.Fatal("routine oracle metadata", col)
			}
		} else {
			// Native scalar calls retain unknown nullability; stored view
			// columns use the caller's explicit source-column expectation.
			wantScan := reflect.TypeFor[int64]()
			if typ == "FLOAT8" {
				wantScan = reflect.TypeFor[float64]()
			} else if typ == "BOOL" {
				wantScan = reflect.TypeFor[bool]()
			}
			_, lengthOK := col.Length()
			_, _, decimalOK := col.DecimalSize()
			nullable, nullableOK := col.Nullable()
			if col.ScanType() != wantScan || lengthOK || decimalOK || nullable != nativeNullable || nullableOK != nativeNullableKnown {
				t.Fatal("routine native metadata", col)
			}
		}
	}
}

func routineDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, q, code, message, detail string, query bool) {
	t.Helper()
	native, postgres := routineExpectedDiagnostic(q, code, message, detail)
	for _, prepared := range []bool{false, true} {
		stage := "execute"
		var err error
		if prepared {
			var stmt *sql.Stmt
			stmt, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				stage = "prepare"
			} else {
				if query {
					var rows *sql.Rows
					rows, err = stmt.QueryContext(p.Ctx)
					if err == nil {
						for rows.Next() {
						}
						err = rows.Err()
						rows.Close()
					}
				} else {
					_, err = stmt.ExecContext(p.Ctx)
				}
				stmt.Close()
			}
		} else if query {
			var rows *sql.Rows
			rows, err = c.QueryContext(p.Ctx, q)
			if err == nil {
				for rows.Next() {
				}
				err = rows.Err()
				rows.Close()
			}
		} else {
			_, err = c.ExecContext(p.Ctx, q)
		}
		if err == nil {
			t.Errorf("expected diagnostic: %s", q)
			continue
		}
		wantStage := "execute"
		if prepared && (code == "42883" || code == "42725") {
			wantStage = "prepare"
		}
		fixture.CheckDiagnostic(t, err, fixture.Step{Diagnostic: &native, PostgresDiagnostic: &postgres, ErrorStage: wantStage}, stage)
	}
}

// PostgreSQL fields are actual pinned18.6 observations from tests-1.md/tests-2.md.
// Native does not inline SQL functions: execution Context identifies statement1,
// rather than falsely claiming PostgreSQL's optimizer/inlining phase.
func routineExpectedDiagnostic(q, code, message, detail string) (fixture.Diagnostic, fixture.Diagnostic) {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	switch q {
	case "SELECT routine_narrow(4::bigint)", "SELECT routine_missing(1)", "SELECT add1()", "SELECT add1(true)", "SELECT 'é' AS label, routine_missing(1)":
		d.Position = "8"
		if q == "SELECT 'é' AS label, routine_missing(1)" {
			d.Position = "22"
		}
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	case "SELECT routine_pick('4')":
		d.Position = "8"
		d.Hint = "Could not choose a best candidate function. You might need to add explicit type casts."
	case "CREATE FUNCTION routine_bad_result() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$":
		d.Context = "SQL function \"routine_bad_result\""
	case "CREATE FUNCTION routine_bad_width() RETURNS integer LANGUAGE SQL AS $$SELECT 1,2$$":
		d.Context = "SQL function \"routine_bad_width\""
	case "CREATE FUNCTION routine_bad_body() RETURNS integer LANGUAGE SQL AS $$SELECT 1 +$$":
		d.Position = "80"
	case "CREATE FUNCTION routine_bad_unicode() RETURNS integer LANGUAGE SQL AS $$SELECT 'é', 1 +$$":
		d.Position = "88"
	case "SELECT routine_sort_target()":
		d.Context = "SQL function \"routine_sort_target\" statement 1"
	case "SELECT routine_authorized() AS v":
		if message == "permission denied for table routine_private" {
			d.Context = "SQL function \"routine_authorized\" statement 1"
		}
	}
	pg := d
	var runtimeName string
	switch q {
	case "SELECT routine_fail()":
		runtimeName = "routine_fail"
	case "SELECT routine_divzero()":
		runtimeName = "routine_divzero"
	case "SELECT routine_overflow()":
		runtimeName = "routine_overflow"
	}
	if runtimeName != "" {
		d.Context = "SQL function \"" + runtimeName + "\" statement 1"
		pg.Context = "SQL function \"" + runtimeName + "\" during inlining"
	}
	return d, pg
}
