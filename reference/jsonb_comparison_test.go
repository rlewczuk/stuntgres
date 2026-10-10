package reference_test

import (
	"testing"
)

func TestReferenceJSONBComparison(t *testing.T) {
	cases := []struct {
		name, left, right string
		sign              int
	}{
		{"numeric_scale", "1", "1.00", 0}, {"nested_numeric", `{"x":[1,2.00]}`, `{"x":[1.0,2]}`, 0},
		{"empty_root", "[]", "null", -1}, {"null_string", "null", `"a"`, -1}, {"string_number", `"z"`, "0", -1},
		{"number_boolean", "999", "false", -1}, {"boolean_array", "true", "[0]", -1}, {"array_object", "[0]", "{}", -1},
		{"false_true", "false", "true", -1}, {"string_c", `"Z"`, `"a"`, -1},
		{"array_count", "[9]", "[0,0]", -1}, {"object_count", `{"z":9}`, `{"a":0,"b":0}`, -1},
		{"object_storage_keys", `{"aa":1,"c":1}`, `{"b":1,"d":1}`, 1},
		{"array_recursive", "[1,[2]]", "[1,[3]]", -1},
		{"nested_empty_array", "[[]]", "[null]", 1},
		{"object_empty_array", `{"x":[]}`, `{"x":null}`, 1},
		{"nested_container_count", "[[9]]", "[[0,0]]", -1},
		{"array_order", "[1,2]", "[2,1]", -1}, {"array_duplicates", "[1]", "[1,1]", -1},
		{"object_key_normalization", `{"a":1,"\u0061":2}`, `{"a":2}`, 0},
		{"root_array_scalar", "[null]", "null", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE jb_compare(id INT,v JSONB)", 0))
				run(referenceExec("INSERT INTO jb_compare VALUES(1,$1),(2,$2),(3,NULL)", 3, tc.left, tc.right))
				for _, prepared := range []bool{false, true} {
					for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+op, func(t *testing.T) {
							run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
							value := false
							switch op {
							case "=":
								value = tc.sign == 0
							case "<>":
								value = tc.sign != 0
							case "<":
								value = tc.sign < 0
							case "<=":
								value = tc.sign <= 0
							case ">":
								value = tc.sign > 0
							case ">=":
								value = tc.sign >= 0
							}
							s := referenceSelect("SELECT $1::jsonb "+op+" $2::jsonb AS v", []string{"v"}, []string{"BOOL"}, [][]any{{value}}, tc.left, tc.right)
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
							s = referenceSelect("SELECT "+jsonReferenceQuote(tc.left)+"::jsonb "+op+" "+jsonReferenceQuote(tc.right)+"::jsonb AS v", []string{"v"}, []string{"BOOL"}, [][]any{{value}})
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
							var ids [][]any
							if value {
								ids = append(ids, []any{int64(1)})
							}
							equalRight := op == "=" || op == "<=" || op == ">="
							if equalRight {
								ids = append(ids, []any{int64(2)})
							}
							s = referenceSelect("SELECT id FROM jb_compare WHERE v "+op+" $1::jsonb ORDER BY id", []string{"id"}, []string{"INT4"}, ids, tc.right)
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
							s = referenceSelect("SELECT id FROM jb_compare WHERE v "+op+" "+jsonReferenceQuote(tc.right)+"::jsonb ORDER BY id", []string{"id"}, []string{"INT4"}, ids)
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
							s = referenceSelect("SELECT NULL::jsonb "+op+" $1::jsonb AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, tc.right)
							if prepared {
								s = referencePrepared(s)
							}
							run(s)
						})
					}
				}
			}
		})
	}
}

func TestReferenceJSONBOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE jb_order(id INT,v JSONB)", 0))
		inputs := []any{`{}`, `[0]`, `true`, `false`, `1.00`, `1`, `"a"`, `"Z"`, `null`, `[]`, nil}
		for i, v := range inputs {
			run(referenceExec("INSERT INTO jb_order VALUES($1,$2)", 1, i, v))
		}
		want := [][]any{{int64(9)}, {int64(8)}, {int64(7)}, {int64(6)}, {int64(4)}, {int64(5)}, {int64(3)}, {int64(2)}, {int64(1)}, {int64(0)}, {int64(10)}}
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT id FROM jb_order ORDER BY v,id", []string{"id"}, []string{"INT4"}, want)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
	}
}

func jsonbReferenceGrouping(t *testing.T, p *referencePair, backend referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
	run(referenceExec("CREATE TABLE jb_groups(id INT,v JSONB)", 0))
	run(referenceExec(`INSERT INTO jb_groups VALUES(1,'1'),(2,'1.00'),(3,'1e0'),(4,'{"x":[1]}'),(5,'{"x":[1.0]}'),(6,'null'),(7,NULL),(8,NULL)`, 8))
	for _, prepared := range []bool{false, true} {
		s := referenceSelect("SELECT count(*) AS n FROM jb_groups GROUP BY v", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(2)}})
		s.bag = true
		if prepared {
			s = referencePrepared(s)
		}
		run(s)
		// Group keys need not preserve one particular numeric display scale.
		s = referenceSelect("SELECT DISTINCT v::jsonb = '1'::jsonb AS same FROM jb_groups", []string{"same"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		s.bag = true
		if prepared {
			s = referencePrepared(s)
		}
		run(s)
	}
	run(referenceExec("CREATE TABLE jb_unique(id INT,v JSONB CONSTRAINT jb_unique_v UNIQUE)", 0))
	run(referenceExec("CREATE INDEX jb_unique_order ON jb_unique USING btree(v)", 0))
	run(referenceExec(`INSERT INTO jb_unique VALUES(1,'{"x":[1]}')`, 1))
	want := controlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "jb_unique_v"`)
	want.Detail = `Key (v)=({"x": [1.0]}) already exists.`
	want.Schema = "public"
	want.Table = "jb_unique"
	want.Constraint = "jb_unique_v"
	run(referenceErrorDiagnostic(`INSERT INTO jb_unique VALUES(2,'false'),(3,'{"x":[1.0]}')`, want))
	run(referenceSelect("SELECT id FROM jb_unique ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	run(referencePrepared(referenceExec(`INSERT INTO jb_unique VALUES(2,'false')`, 1)))
	run(referenceExec(`UPDATE jb_unique SET v='[2]' WHERE id=2`, 1))
	run(referencePrepared(referenceSelect("SELECT id FROM jb_unique WHERE v=$1::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}, `[2.0]`)))
	run(referenceExec("DELETE FROM jb_unique WHERE v=$1::jsonb", 1, `{"x":[1.00]}`))
	run(referenceExec(`INSERT INTO jb_unique VALUES(3,'{"x":[1.0]}')`, 1))
	run(referenceSelect("SELECT id FROM jb_unique ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
	run(referenceExec("CREATE TABLE jb_distinct(v JSONB)", 0))
	run(referenceExec(`INSERT INTO jb_distinct VALUES('{"x":[1],"z":2}'),('{"z":2,"x":[1e0]}'),('null'),(NULL),(NULL)`, 5))
	for _, prepared := range []bool{false, true} {
		s := referenceSelect("SELECT DISTINCT v FROM jb_distinct", []string{"v"}, []string{"JSONB"}, [][]any{{`{"x": [1], "z": 2}`}, {"null"}, {nil}})
		s.bag = true
		if prepared {
			s = referencePrepared(s)
		}
		run(s)
	}
}
func TestReferenceJSONBGroupingAndBTree(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		jsonbReferenceGrouping(t, p, backend)
	}
}
