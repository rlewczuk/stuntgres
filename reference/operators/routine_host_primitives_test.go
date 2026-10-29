package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostPrimitives(t *testing.T) {
	hostPrimitiveScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, sig, result, program, python, typ string
			inputs, outputs                         []any
		}{
			{"hp_bool", "v boolean", "boolean", "bool-not-v1", "return None if v is None else not v", "BOOL", []any{false, true, nil}, []any{true, false, nil}},
			{"hp_default", "v boolean", "boolean", "bool-default-v1", "return None if v is None else not v", "BOOL", []any{true, false, nil}, []any{false, true, nil}},
			{"hp_i4", "v integer", "integer", "int4-offset-v1", "return None if v is None else v + 1", "INT4", []any{int64(0), int64(-1), int64(2147483646), nil}, []any{int64(1), int64(0), int64(2147483647), nil}},
			{"hp_dec", "v integer", "integer", "int4-decrement-v1", "return None if v is None else v - 1", "INT4", []any{int64(-2147483647), int64(1)}, []any{int64(-2147483648), int64(0)}},
			{"hp_i8", "v bigint", "bigint", "int8-offset-v1", "return None if v is None else v + 5", "INT8", []any{int64(0), int64(-5), int64(-9223372036854775808), int64(9223372036854775802), nil}, []any{int64(5), int64(0), int64(-9223372036854775803), int64(9223372036854775807), nil}},
			{"hp_text", "v text", "text", "text-mark-v1", "return None if v is None else v + '!'", "TEXT", []any{"", "λ", nil}, []any{"!", "λ!", nil}},
			{"hp_strip", "v text", "text", "text-strip-v1", "return None if v is None else (v[:-1] if v.endswith('!') else v)", "TEXT", []any{"!", "λ!", nil}, []any{"", "λ", nil}},
			{"hp_null", "v text", "text", "text-null-v1", "return 'handled-null' if v is None else v + '!'", "TEXT", []any{nil, ""}, []any{"handled-null", "!"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hostPrimitiveFunction(t, p, b, c, tc.name, tc.sig, tc.result, tc.program, tc.python, "")
				cast := tc.result
				q := "SELECT " + tc.name + "($1::" + cast + ") AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for i, input := range tc.inputs {
					want := [][]any{{tc.outputs[i]}}
					hostRows(t, p, b, c, q, []string{"v"}, []string{tc.typ}, want, input)
					hostRows(t, p, b, held, q, []string{"v"}, []string{tc.typ}, want, input)
				}
				literal := "0"
				if tc.result == "boolean" {
					literal = "false"
				}
				if tc.result == "text" {
					literal = "''"
				}
				hostPrimitiveMetadata(t, p, b, c, "SELECT "+tc.name+"("+literal+"::"+cast+") AS v", tc.typ)
				hostPrimitiveMetadata(t, p, b, c, "SELECT "+tc.name+"(NULL::"+cast+") AS v", tc.typ)
				hostPrimitiveMetadata(t, p, b, c, "SELECT "+tc.name+"(NULL::"+cast+") AS v WHERE false", tc.typ)
			})
		}
		hostPrimitiveFunction(t, p, b, c, "hp_strict", "v text", "text", "text-null-v1", "return 'handled-null' if v is None else v + '!'", "STRICT")
		hostRows(t, p, b, c, "SELECT hp_strict(NULL::text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}})
		strictQuery := "SELECT hp_strict($1::text) AS v"
		strictStatement, err := c.PrepareContext(p.Ctx, strictQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer strictStatement.Close()
		strictHeld := fixture.HeldStatement{Executor: c, Statement: strictStatement, SQL: strictQuery}
		for _, input := range []any{nil, "", nil, "λ"} {
			var output any
			if input != nil {
				output = input.(string) + "!"
			}
			hostRows(t, p, b, c, strictQuery, []string{"v"}, []string{"TEXT"}, [][]any{{output}}, input)
			hostRows(t, p, b, strictHeld, strictQuery, []string{"v"}, []string{"TEXT"}, [][]any{{output}}, input)
		}
		for _, tc := range []struct {
			name, sig, result, program, python, q, typ string
			args                                       [][]any
			want                                       []any
		}{
			{"hp_zero", "", "bigint", "zero-v1", "return 17", "SELECT hp_zero() AS v", "INT8", [][]any{{}, {}}, []any{int64(17), int64(17)}},
			{"hp_add", "a bigint,b bigint", "bigint", "int8-add-v1", "return None if a is None or b is None else a+b", "SELECT hp_add($1::bigint,$2::bigint) AS v", "INT8", [][]any{{int64(2), int64(3)}, {int64(-2), int64(2)}, {int64(-9223372036854775808), int64(0)}, {int64(9223372036854775807), int64(0)}, {nil, int64(2)}}, []any{int64(5), int64(0), int64(-9223372036854775808), int64(9223372036854775807), nil}},
			{"hp_mixed", "a boolean,b integer,c bigint,d text", "text", "mixed-v1", "return None if a is None or b is None or c is None or d is None else str(a).lower()+':'+str(b)+':'+str(c)+':'+d", "SELECT hp_mixed($1::boolean,$2::integer,$3::bigint,$4::text) AS v", "TEXT", [][]any{{false, int64(0), int64(7), ""}, {true, int64(-2), int64(9), "x"}}, []any{"false:0:7:", "true:-2:9:x"}},
			{"hp_five", "a bigint,b bigint,c bigint,d bigint,e bigint", "bigint", "int8-sum5-v1", "return None if any(x is None for x in [a,b,c,d,e]) else a+b+c+d+e", "SELECT hp_five($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5::bigint) AS v", "INT8", [][]any{{int64(1), int64(2), int64(3), int64(4), int64(5)}, {int64(0), int64(-1), int64(2), int64(0), int64(4)}}, []any{int64(15), int64(5)}},
		} {
			hostPrimitiveFunction(t, p, b, c, tc.name, tc.sig, tc.result, tc.program, tc.python, "")
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
			for i, args := range tc.args {
				hostRows(t, p, b, c, tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want[i]}}, args...)
				hostRows(t, p, b, held, tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want[i]}}, args...)
			}
			stmt.Close()
		}
	})
}

// Check actual raw carriers and every exposed metadata property, also on empty
// results. Retain ColumnType objects beyond cursor closure.
func hostPrimitiveMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q, typ string) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != 1 {
		t.Fatalf("columns=%v err=%v", cols, err)
	}
	col := cols[0]
	check := func() {
		scan := reflect.TypeFor[string]()
		switch typ {
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if b.PG {
				scan = reflect.TypeFor[int32]()
			}
		case "INT8":
			scan = reflect.TypeFor[int64]()
		}
		if col.Name() != "v" || col.DatabaseTypeName() != typ || col.ScanType() != scan {
			t.Errorf("metadata %q/%q/%v want v/%s/%v", col.Name(), col.DatabaseTypeName(), col.ScanType(), typ, scan)
		}
		if n, ok := col.Nullable(); n || ok {
			t.Errorf("nullable=%v/%v", n, ok)
		}
		wantLength, wantLengthKnown := int64(0), false
		if typ == "TEXT" {
			// Pinned PG reports unlimited TEXT; native Usage.Length exposes
			// that same independently specified unbounded character length.
			wantLength, wantLengthKnown = int64(9223372036854775807), true
		}
		if n, ok := col.Length(); n != wantLength || ok != wantLengthKnown {
			t.Errorf("length=%d/%v want %d/%v", n, ok, wantLength, wantLengthKnown)
		}
		if n, s, ok := col.DecimalSize(); n != 0 || s != 0 || ok {
			t.Errorf("decimal=%d/%d/%v", n, s, ok)
		}
	}
	check()
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw == nil {
			continue
		}
		want := reflect.TypeFor[string]()
		switch typ {
		case "BOOL":
			want = reflect.TypeFor[bool]()
		case "INT4", "INT8":
			want = reflect.TypeFor[int64]()
		}
		if reflect.TypeOf(raw) != want {
			t.Errorf("raw=%T want %v", raw, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check()
}
