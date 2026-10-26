package reference_test

import (
	"database/sql"
	"reflect"
	"slices"
	"testing"

	"stuntgres/reference/fixture"
)

const proceduresNoMatchHint = "No procedure matches the given name and argument types. You might need to add explicit type casts."

func proceduresScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

// CALL outputs use declared typmod-free descriptors, not body expression aliases.
// Every accessor is checked before row demand, including absent descriptors.
func proceduresMetadata(b fixture.Backend, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		got, err := rows.Columns()
		if err != nil || !slices.Equal(got, names) {
			t.Fatalf("CALL columns %v %v want %v", got, err, names)
		}
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != len(names) {
			t.Fatal(cols, err)
		}
		for i, c := range cols {
			scan := reflect.TypeFor[string]()
			switch types[i] {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if b.PG {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "TEXT":
			case "_INT4", "":
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatalf("unspecified CALL type %q", types[i])
			}
			length, lengthOK := c.Length()
			nullable, nullableOK := c.Nullable()
			precision, scale, decimalOK := c.DecimalSize()
			wantLength, wantLengthOK := int64(0), false
			if types[i] == "TEXT" {
				wantLength, wantLengthOK = resultsTextMetadata.Length, resultsTextMetadata.LengthKnown
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || nullable || nullableOK || precision != 0 || scale != 0 || decimalOK {
				t.Errorf("CALL column %d: name=%q type=%q scan=%v length=%d/%t nullable=%t/%t decimal=%d,%d/%t; want %s/%s scan=%v length=%d/%t unknown nullability/decimal", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, nullable, nullableOK, precision, scale, decimalOK, names[i], types[i], scan, wantLength, wantLengthOK)
			}
		}
	}
}

func proceduresStep(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.CheckMetadata = proceduresMetadata(b, names, types)
	s.RawTypes = append([]string(nil), types...)
	for i, typ := range s.RawTypes {
		if typ == "" {
			s.RawTypes[i] = "RECORD"
		}
	}
	return s
}

func proceduresBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any, args ...any) {
	t.Helper()
	s := proceduresStep(b, q, names, types, values, args...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func proceduresHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, names, types []string, inputs [][]any, values [][][]any) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, args := range inputs {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, proceduresStep(b, q, names, types, values[i], args...))
	}
}

func proceduresDiagnostic(code, message string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position = fixture.Position(position)
	return d
}

func proceduresMissing(signature string) fixture.Diagnostic {
	d := proceduresDiagnostic("42883", "procedure "+signature+" does not exist", 6)
	d.Hint = proceduresNoMatchHint
	return d
}

func proceduresCount(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string, n int64) {
	t.Helper()
	resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM "+table, n)
}

func proceduresMixSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c,
		"CREATE PROCEDURE pc_mix(OUT first integer,IN x integer,INOUT total bigint,OUT note text,IN bonus integer DEFAULT 7) LANGUAGE SQL AS $$SELECT x+bonus,total+x,'done'::text$$",
		"CREATE PROCEDURE pc_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 7'",
		"CREATE PROCEDURE pc_noout(IN delta integer) LANGUAGE SQL AS 'SELECT delta'",
		"CREATE PROCEDURE pc_array(OUT answer integer[],VARIADIC xs integer[]) LANGUAGE SQL AS 'SELECT xs'")
}

func proceduresMixCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	names, types := []string{"first", "total", "note"}, []string{"INT4", "INT8", "TEXT"}
	proceduresBoth(t, p, b, c, "CALL pc_mix(NULL,4,10,NULL)", names, types, [][]any{{int64(11), int64(14), "done"}})
	proceduresHeld(t, p, b, c, "CALL pc_mix(NULL,$1::integer,$2::bigint,NULL,bonus=>$3::integer)", names, types, [][]any{{4, 10, 3}, {8, 20, 2}, {4, nil, 3}}, [][][]any{{{int64(7), int64(14), "done"}}, {{int64(10), int64(28), "done"}}, {{int64(7), nil, "done"}}})
	proceduresBoth(t, p, b, c, "CALL pc_out(1/0)", []string{"answer"}, []string{"INT4"}, [][]any{{int64(7)}})
	proceduresBoth(t, p, b, c, "CALL pc_array(NULL,1,NULL,3)", []string{"answer"}, []string{"_INT4"}, [][]any{{"{1,NULL,3}"}})
}

// The roster is bounded to independent released declarations and the shared
// procedure setup. Replay callers reuse it without deriving expected counts from
// the backend being checked.
func proceduresDefaultCountsCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsBothMetadata(t, p, b, c,
		"SELECT oid::bigint AS id,pronargdefaults::integer AS defaults FROM pg_proc WHERE oid IN (40,1397,3717,3960,4007) ORDER BY oid",
		[]string{"id", "defaults"}, []string{"INT8", "INT4"},
		[][]any{{int64(40), int64(0)}, {int64(1397), int64(0)}, {int64(3717), int64(0)}, {int64(3960), int64(1)}, {int64(4007), int64(2)}},
		[]resultsNativeColumn{{}, {}})
	resultsBothMetadata(t, p, b, c,
		"SELECT proname::text AS name,pronargdefaults::integer AS defaults FROM pg_proc WHERE proname IN ('pc_array','pc_mix','pc_noout','pc_out') ORDER BY proname",
		[]string{"name", "defaults"}, []string{"TEXT", "INT4"},
		[][]any{{"pc_array", int64(0)}, {"pc_mix", int64(1)}, {"pc_noout", int64(0)}, {"pc_out", int64(0)}},
		[]resultsNativeColumn{resultsTextMetadata, {}})
}
