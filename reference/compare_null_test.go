//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceCompareNullMatrices(t *testing.T) {
	p := expressionReferenceSetup(t)
	cases := []referenceStep{
		referenceSelect("SELECT id,v IS NULL,v IS NOT NULL,v ISNULL,v NOTNULL FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), false, true, false, true}, {int64(2), true, false, true, false}, {int64(3), false, true, false, true}, {int64(4), false, true, false, true}}),
		referenceSelect("SELECT id,f IS TRUE,f IS NOT TRUE,f IS FALSE,f IS NOT FALSE,f IS UNKNOWN,f IS NOT UNKNOWN FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, false, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), false, true, true, false, false, true}, {int64(4), true, false, false, true, false, true}}),
		referenceSelect("SELECT v=NULL FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}, {nil}}),
		referenceSelect("SELECT num_nulls(v),v IS NULL FROM t ORDER BY id", []string{"num_nulls", "?column?"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(0), false}, {int64(0), false}}),
	}
	variants := []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"}
	truth := [][]bool{{true, false, false, true, false, true}, {false, true, true, false, false, true}, {false, true, false, true, true, false}}
	for i, literal := range []string{"TRUE", "FALSE", "NULL"} {
		for j, variant := range variants {
			want := [][]any{}
			for range 4 {
				want = append(want, []any{truth[i][j]})
			}
			cases = append(cases, referenceSelect("SELECT "+literal+" "+variant+" FROM t", []string{"?column?"}, []string{"BOOL"}, want))
		}
	}
	for i, predicate := range []string{"v IS NULL", "v IS NOT NULL", "v ISNULL", "v NOTNULL", "f IS TRUE", "f IS NOT TRUE", "f IS FALSE", "f IS NOT FALSE", "f IS UNKNOWN", "f IS NOT UNKNOWN"} {
		ids := [][]int64{{2}, {1, 3, 4}, {2}, {1, 3, 4}, {1, 4}, {2, 3}, {3}, {1, 2, 4}, {2}, {1, 3, 4}}[i]
		want := [][]any{}
		for _, id := range ids {
			want = append(want, []any{id})
		}
		cases = append(cases, referenceSelect("SELECT id FROM t WHERE "+predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, want))
	}
	for _, tc := range []struct {
		left, right string
		distinct    bool
	}{
		{"NULL", "NULL", false}, {"NULL", "1", true}, {"1", "NULL", true}, {"1", "1", false}, {"1", "2", true},
		{"'x'", "'x'", false}, {"'x'", "'y'", true}, {"TRUE", "FALSE", true}, {"TRUE", "TRUE", false},
		{"1", "'1'", false}, {"'1'", "1", false}, {"(NULL)", "NULL", false}, {"v", "v", false}, {"v", "b", true}, {"b", "v", true},
	} {
		want := [][]any{}
		for range 4 {
			want = append(want, []any{tc.distinct, !tc.distinct})
		}
		cases = append(cases, referenceSelect("SELECT "+tc.left+" IS DISTINCT FROM "+tc.right+","+tc.left+" IS NOT DISTINCT FROM "+tc.right+" FROM t", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, want))
	}
	for _, tc := range []struct {
		expr string
		ids  []int64
	}{
		{"v IS DISTINCT FROM 1", []int64{2, 3}}, {"1 IS DISTINCT FROM v", []int64{2, 3}},
		{"v IS NOT DISTINCT FROM 1", []int64{1, 4}}, {"1 IS NOT DISTINCT FROM v", []int64{1, 4}},
		{"v IS DISTINCT FROM NULL", []int64{1, 3, 4}}, {"NULL IS DISTINCT FROM v", []int64{1, 3, 4}},
		{"v IS NOT DISTINCT FROM NULL", []int64{2}}, {"NULL IS NOT DISTINCT FROM v", []int64{2}},
		{"v=NULL", nil}, {"(v=NULL) IS UNKNOWN", []int64{1, 2, 3, 4}}, {"v=1 IS TRUE", []int64{1, 4}}, {"(v=1) IS NOT TRUE", []int64{2, 3}},
		{"TRUE IS NULL IS TRUE", nil}, {"v IS NULL ISNULL", nil},
		{"v IS NULL AND TRUE", []int64{2}}, {"NOT v IS NULL", []int64{1, 3, 4}},
		{"v IS NULL OR f IS TRUE", []int64{1, 2, 4}}, {"TRUE OR FALSE", []int64{1, 2, 3, 4}},
		{"v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL", []int64{1, 3, 4}},
		{"(f AND TRUE) IS UNKNOWN", []int64{2}},
		{"(f OR FALSE) IS NOT TRUE", []int64{2, 3}},
		{"(NOT f) IS DISTINCT FROM FALSE", []int64{2, 3}},
	} {
		want := [][]any{}
		for _, id := range tc.ids {
			want = append(want, []any{id})
		}
		cases = append(cases, referenceSelect("SELECT id FROM t WHERE "+tc.expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, want))
	}
	for _, tc := range []struct {
		expr   string
		values []bool
	}{
		{"(v IS NULL) IS TRUE", []bool{false, true, false, false}},
		{"(v IS DISTINCT FROM 1) IS FALSE", []bool{true, false, false, true}},
		{"TRUE IS DISTINCT FROM (v IS NULL)", []bool{true, false, true, true}},
		{"(TRUE IS NULL) IS TRUE", []bool{false, false, false, false}},
		{"TRUE IS NULL IS TRUE", []bool{false, false, false, false}},
		{"v IS NULL ISNULL", []bool{false, false, false, false}},
		{"'yes' IS TRUE", []bool{true, true, true, true}}, {"'null' IS NULL", []bool{false, false, false, false}},
		{"(v=1) IS TRUE", []bool{true, false, false, true}},
		{"(v=NULL) IS UNKNOWN", []bool{true, true, true, true}},
		{"(v=v) IS TRUE", []bool{true, false, true, true}},
		{"v IS NULL AND TRUE", []bool{false, true, false, false}},
		{"NOT v IS NULL", []bool{true, false, true, true}},
		{"v IS NULL OR f IS TRUE", []bool{true, true, false, true}},
		{"v<2 IS TRUE OR NOT f IS UNKNOWN AND v IS NOT NULL", []bool{true, false, true, true}},
		{"(f AND TRUE) IS UNKNOWN", []bool{false, true, false, false}},
		{"(f OR FALSE) IS NOT TRUE", []bool{false, true, true, false}},
		{"(NOT f) IS DISTINCT FROM FALSE", []bool{false, true, true, false}},
	} {
		want := [][]any{}
		for _, v := range tc.values {
			want = append(want, []any{v})
		}
		cases = append(cases, referenceSelect("SELECT "+tc.expr+" FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, want))
	}
	for _, step := range cases {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE empty(v INT)", 0))
	step := referenceSelect("SELECT v IS NULL FROM empty", []string{"?column?"}, []string{"BOOL"}, nil)
	p.step(t, step)
	p.step(t, referencePrepared(step))
}

func TestReferenceCompareNullAnyInteraction(t *testing.T) {
	p := expressionReferenceSetup(t)
	for i, arg := range []any{true, false, nil} {
		row := [][]any{{true, int64(0), false}, {false, int64(0), false}, {false, int64(1), true}}[i]
		step := referenceSelect("SELECT $1 IS TRUE,num_nulls($1),$1 IS NULL FROM t", []string{"?column?", "num_nulls", "?column?"}, []string{"BOOL", "INT4", "BOOL"}, [][]any{row, row, row, row}, arg)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceCompareNullDiagnostics(t *testing.T) {
	p := expressionReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE empty(v INT,s TEXT,b BIGINT)", 0))
	cases := []struct {
		q    string
		d    referenceDiagnostic
		args []any
	}{
		{"SELECT /*é*/ 1 IS NOT UNKNOWN FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS NOT UNKNOWN must be type boolean, not type integer", Position: "14"}, nil},
		{"SELECT v IS NULL AND 1 FROM t", referenceDiagnostic{Code: "42804", Message: "argument of AND must be type boolean, not type integer", Position: "22"}, nil},
		{"SELECT TRUE OR 1 IS TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "16"}, nil},
		{"SELECT FALSE AND missing IS NULL FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"}, nil},
		{"SELECT v IS DISTINCT FROM TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT v IS NOT DISTINCT FROM TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "10", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT TRUE IS DISTINCT FROM v FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean = integer", Position: "13", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}, nil},
		{"SELECT 1 IS DISTINCT FROM 'oops' FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "27"}, nil},
		{"SELECT 'oops' IS DISTINCT FROM 1 FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "8"}, nil},
		{"SELECT 'truth' IS TRUE FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "8"}, nil},
		{"SELECT missing IS NULL FROM t", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}, nil},
		{"SELECT 1 IS DISTINCT FROM 1 IS TRUE FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "IS"`, Position: "29"}, nil},
		{"SELECT $1 IS NULL,$1 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true}},
		{"SELECT num_nulls($1),$1 IS NULL,$1 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "18"}, []any{true}},
		{"SELECT $1 IS NULL,num_nulls($1),$1 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true}},
		{"SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $2", Position: "18"}, []any{true, true}},
		{"SELECT $1 IS NULL,num_nulls($2),$1 IS TRUE,$2 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{true, true}},
		{"SELECT num_nulls($2),$1 IS NULL,$1 IS TRUE FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "22"}, []any{true, nil}},
		{"SELECT $1 IS NULL,$1 FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "8"}, []any{"x"}},
		{"SELECT $1,$1 IS NULL FROM t", referenceDiagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "11"}, []any{"x"}},
		{"SELECT $1 FROM t WHERE v IS DISTINCT FROM $1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{1}},
		{"SELECT $1 IS DISTINCT FROM v,$1 IS TRUE FROM t", referenceDiagnostic{Code: "42804", Message: "argument of IS TRUE must be type boolean, not type integer", Position: "30"}, []any{1}},
	}
	for _, variant := range []string{"IS TRUE", "IS NOT TRUE", "IS FALSE", "IS NOT FALSE", "IS UNKNOWN", "IS NOT UNKNOWN"} {
		for _, table := range []string{"t", "empty"} {
			for _, input := range []struct{ expr, typ string }{{"1", "integer"}, {"s", "text"}, {"b", "bigint"}} {
				cases = append(cases, struct {
					q    string
					d    referenceDiagnostic
					args []any
				}{"SELECT " + input.expr + " " + variant + " FROM " + table, referenceDiagnostic{Code: "42804", Message: "argument of " + variant + " must be type boolean, not type " + input.typ, Position: "8"}, nil})
			}
		}
	}
	for _, expr := range []string{"$1 IS NULL", "$1 IS NOT NULL", "$1 ISNULL", "$1 NOTNULL", "$1 IS DISTINCT FROM NULL", "$1 IS NOT DISTINCT FROM NULL", "NULL IS DISTINCT FROM $1", "NULL IS NOT DISTINCT FROM $1"} {
		cases = append(cases, struct {
			q    string
			d    referenceDiagnostic
			args []any
		}{"SELECT " + expr + " FROM t", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}, []any{true}})
	}
	for _, tc := range cases {
		step := referenceImplicitError(tc.q, tc.d, tc.args...)
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	bad := referenceImplicitError("SELECT $1 IS TRUE FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`}, "truth")
	bad.selectRows = true
	bad.errorStage = "execute"
	pg := *bad.diagnostic
	pg.Context = "unnamed portal parameter $1 = '...'"
	bad.postgresDiagnostic = &pg
	p.step(t, bad)
	p.step(t, referencePrepared(bad))
	native := referenceImplicitError("SELECT $1 IS NULL FROM t", referenceDiagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"})
	native.selectRows = true
	native.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
	simple := referenceImplicitError("SELECT $1 IS NULL FROM t", referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "8"})
	simple.selectRows = true
	simple.errorStage = "execute"
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, simple)
}

func TestReferenceCompareNullHeldParameters(t *testing.T) {
	p := expressionReferenceSetup(t)
	for _, q := range []string{"SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 ORDER BY id", "SELECT id FROM t WHERE $1 IS NOT DISTINCT FROM v ORDER BY id", "SELECT id FROM t WHERE v IS NOT DISTINCT FROM $1 AND (v=$1) IS NOT FALSE ORDER BY id", "SELECT $1 IS TRUE,$1 IS NULL FROM t"} {
		observed := [][][][]any{}
		for _, db := range []*sql.DB{p.memory, p.postgres} {
			stmt, err := db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
			args := []any{1, 2, nil}
			names, types := []string{"id"}, []string{"INT4"}
			wants := [][][]any{{{int64(1)}, {int64(4)}}, {{int64(3)}}, {{int64(2)}}}
			if q == "SELECT $1 IS TRUE,$1 IS NULL FROM t" {
				args = []any{true, false, nil}
				names = []string{"?column?", "?column?"}
				types = []string{"BOOL", "BOOL"}
				wants = [][][]any{{{true, false}, {true, false}, {true, false}, {true, false}}, {{false, false}, {false, false}, {false, false}, {false, false}}, {{false, true}, {false, true}, {false, true}, {false, true}}}
			}
			executions := [][][]any{}
			for i, arg := range args {
				rows, err := stmt.QueryContext(p.ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				gotNames, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(gotNames, names) {
					t.Fatal(gotNames, err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for j, typ := range types {
					if metadata[j].DatabaseTypeName() != typ {
						t.Fatal(metadata[j])
					}
				}
				got := [][]any{}
				for rows.Next() {
					values := make([]any, len(names))
					dest := make([]any, len(names))
					for j := range values {
						dest[j] = &values[j]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					got = append(got, values)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if !reflect.DeepEqual(got, wants[i]) {
					t.Fatal("independent held result", got, wants[i])
				}
				executions = append(executions, got)
			}
			stmt.Close()
			observed = append(observed, executions)
		}
		if !reflect.DeepEqual(observed[0], observed[1]) {
			t.Fatal("held backend parity", observed)
		}
	}
	for _, step := range []referenceStep{
		referenceSelect("SELECT id FROM t WHERE v IS DISTINCT FROM $1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(4)}}, nil),
		referenceSelect("SELECT $1 IS DISTINCT FROM $2 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}}, "x", "y"),
		referenceSelect("SELECT $1 IS DISTINCT FROM $2 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}, nil, nil),
		referenceSelect("SELECT $1 IS DISTINCT FROM $1 FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}}, "x"),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceCompareNullParameterErrorPrecedence(t *testing.T) {
	p := expressionReferenceSetup(t)
	for _, tc := range []struct {
		name, query, parameter, code, position string
		args                                   []any
	}{
		{"unresolved competitor", "SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t", "1", "42P08", "8", []any{true, nil}},
		{"gap competitor", "SELECT $2 IS NULL,$2 IS TRUE FROM t", "2", "42P08", "8", []any{nil, true}},
		{"raw NULL rewrite competitor", "SELECT $1 IS DISTINCT FROM NULL,$1 IS TRUE,$2 IS NULL FROM t", "1", "42P08", "8", []any{true, nil}},
		{"unresolved control", "SELECT $1 IS TRUE,$2 IS NULL FROM t", "2", "42P18", "", []any{true, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: "could not determine data type of parameter $" + tc.parameter, Position: tc.position}, tc.args...)
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		})
	}
}
