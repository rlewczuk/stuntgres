package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func resultsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

// Profiles describe native adapter properties of the selected expression/source,
// independently of pq's fixed unknown-nullability and type-length contract.
type resultsNativeColumn struct {
	Length        int64
	LengthKnown   bool
	Nullable      bool
	NullableKnown bool
}

var resultsTextMetadata = resultsNativeColumn{Length: 1<<63 - 1, LengthKnown: true}
var resultsNonNullMetadata = resultsNativeColumn{NullableKnown: true}
var resultsNullableMetadata = resultsNativeColumn{Nullable: true, NullableKnown: true}

func resultsMetadata(b fixture.Backend, names, types []string, stored bool, native ...resultsNativeColumn) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		if len(native) != 0 && len(native) != len(names) {
			t.Fatal("incomplete native column metadata profile")
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, names) {
			t.Fatalf("columns got %v, %v want %v", columns, err, names)
		}
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != len(names) {
			t.Fatal(cols, err)
		}
		for i, col := range cols {
			var scan reflect.Type
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
				scan = reflect.TypeFor[string]()
			case "_INT4":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "RECORD", "":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatalf("unspecified metadata type %q", types[i])
			}
			length, lengthOK := col.Length()
			precision, scale, decimalOK := col.DecimalSize()
			nullable, nullableOK := col.Nullable()
			wantNullable, wantNullableOK := stored && !b.PG, stored && !b.PG
			wantLength, wantLengthOK := int64(0), false
			if b.PG && types[i] == "TEXT" {
				wantLength, wantLengthOK = int64(1<<63-1), true
			}
			if !b.PG && len(native) != 0 {
				wantLength, wantLengthOK = native[i].Length, native[i].LengthKnown
				wantNullable, wantNullableOK = native[i].Nullable, native[i].NullableKnown
			}
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != wantNullable || nullableOK != wantNullableOK {
				t.Errorf("column %d got name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want %s/%s scan=%v length=%d/%t zero unknown decimal nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK, names[i], types[i], scan, wantLength, wantLengthOK, wantNullable, wantNullableOK)
			}
		}
	}
}

func resultsStep(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.CheckMetadata = resultsMetadata(b, names, types, false)
	// Unknown user composite OIDs have empty public type names but retain the
	// same canonical composite raw-value contract as RECORD.
	s.RawTypes = append([]string(nil), types...)
	for i, typ := range s.RawTypes {
		if typ == "" {
			s.RawTypes[i] = "RECORD"
		}
	}
	return s
}

func resultsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any, args ...any) {
	t.Helper()
	resultsBothMetadata(t, p, b, c, q, names, types, values, nil, args...)
}

func resultsBothMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any, native []resultsNativeColumn, args ...any) {
	t.Helper()
	s := resultsStep(b, q, names, types, values, args...)
	s.CheckMetadata = resultsMetadata(b, names, types, false, native...)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func resultsCountBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, count int64) {
	t.Helper()
	resultsBothMetadata(t, p, b, c, q, []string{"v"}, []string{"INT8"}, [][]any{{count}}, []resultsNativeColumn{resultsNonNullMetadata})
}

func resultsStoredBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any) {
	t.Helper()
	s := resultsStep(b, q, names, types, values)
	s.CheckMetadata = resultsMetadata(b, names, types, true)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func resultsHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, names, types []string, inputs [][]any, values [][][]any) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, args := range inputs {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, resultsStep(b, q, names, types, values[i], args...))
	}
}

func resultsError(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, query bool, stage string, want fixture.Diagnostic, args ...any) {
	t.Helper()
	fixture.EnumReferenceErrorChecked(t, p, c, q, query, stage, func(t *testing.T, got fixture.Diagnostic) {
		if got != want {
			t.Errorf("routine result diagnostic query=%q got=%#v want=%#v", q, got, want)
		}
	}, args...)
}

func resultsMultiSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c,
		"CREATE FUNCTION rr_multi(IN x integer, OUT inc integer, OUT twice integer) LANGUAGE SQL AS 'SELECT $1+1 AS body_b,$1*2 AS body_a'",
		"CREATE FUNCTION rr_explicit(IN x integer, OUT inc integer, OUT twice integer) RETURNS record LANGUAGE SQL AS 'SELECT $1+1,$1*2'")
}

func resultsSetSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c,
		"CREATE TYPE rr_pair AS (key integer,val integer)",
		"CREATE TABLE rr_rows(k integer,v integer)",
		"CREATE FUNCTION rr_set(integer) RETURNS SETOF integer LANGUAGE SQL AS 'SELECT v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_record_set(IN n integer,OUT key integer,OUT val integer) RETURNS SETOF record LANGUAGE SQL AS 'SELECT k,v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_table(integer) RETURNS TABLE(key integer,val integer) LANGUAGE SQL AS 'SELECT k AS val,v AS key FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_one_table(integer) RETURNS TABLE(item integer) LANGUAGE SQL AS 'SELECT v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_named_set(integer) RETURNS SETOF rr_pair LANGUAGE SQL AS 'SELECT k,v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_wide_set(integer) RETURNS SETOF bigint LANGUAGE SQL AS 'SELECT v FROM rr_rows WHERE k <= $1 ORDER BY k,v'")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_rows VALUES(1,10),(2,20),(2,20)", 3))
}

func resultsCompositeSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c,
		"CREATE FUNCTION rr_named(integer) RETURNS rr_pair LANGUAGE SQL AS 'SELECT $1,$1*2'",
		"CREATE FUNCTION rr_whole(integer) RETURNS rr_pair LANGUAGE SQL AS 'SELECT ROW($1,$1*2)::rr_pair'",
		"CREATE FUNCTION rr_arg(p rr_pair) RETURNS integer LANGUAGE SQL AS 'SELECT (p).key+(p).val'",
		"CREATE FUNCTION rr_pos_arg(rr_pair) RETURNS integer LANGUAGE SQL AS 'SELECT $1.key+$1.val'",
		"CREATE FUNCTION rr_empty(integer) RETURNS integer LANGUAGE SQL AS 'SELECT v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_empty_pair(integer) RETURNS rr_pair LANGUAGE SQL AS 'SELECT k,v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_empty_record(IN n integer,OUT key integer,OUT val integer) LANGUAGE SQL AS 'SELECT k,v FROM rr_rows WHERE k <= $1 ORDER BY k,v'",
		"CREATE FUNCTION rr_null_pair() RETURNS rr_pair LANGUAGE SQL AS 'SELECT NULL::integer,NULL::integer'",
		"CREATE FUNCTION rr_null_record(OUT key integer,OUT val integer) LANGUAGE SQL AS 'SELECT NULL::integer,NULL::integer'")
}

func resultsMultiCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, name := range []string{"rr_multi", "rr_explicit"} {
		resultsBoth(t, p, b, c, "SELECT * FROM "+name+"(4)", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
	}
	resultsBoth(t, p, b, c, "SELECT * FROM rr_multi(x=>$1::integer)", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}}, 4)
	resultsHeld(t, p, b, c, "SELECT * FROM rr_multi($1::integer)", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{4}, {9}, {4}}, [][][]any{{{int64(5), int64(8)}}, {{int64(10), int64(18)}}, {{int64(5), int64(8)}}})
}

func resultsSetCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, name := range []string{"rr_record_set", "rr_table", "rr_named_set"} {
		q := "SELECT * FROM " + name + "($1::integer) ORDER BY key,val"
		values := [][][]any{{}, {{int64(1), int64(10)}}, {{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}}
		for n := range 3 {
			resultsBoth(t, p, b, c, q, []string{"key", "val"}, []string{"INT4", "INT4"}, values[n], n)
		}
		resultsHeld(t, p, b, c, q, []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{0}, {1}, {2}, {0}, {2}}, [][][]any{values[0], values[1], values[2], values[0], values[2]})
	}
}
