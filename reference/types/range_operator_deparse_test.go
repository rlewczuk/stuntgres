package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Exact presentation expectations follow the existing arithmetic deparse
// contracts; the pinned oracle settles them before native renderer remedies.
// Re-executing the actual emitted expression independently checks its meaning.
func TestReferenceRangeOperatorDeparseGrouping(t *testing.T) {
	for _, tc := range []struct {
		name, typ, expression, compact, pretty string
		a, b, c                                string
		want                                   any
	}{
		{"left_union", "int4range", "(a + b) * c", "((a + b) * c)", "(a + b) * c", "[1,3)", "[3,7)", "[2,4)", "[2,4)"},
		{"right_difference", "int4range", "a - (b - c)", "(a - (b - c))", "a - (b - c)", "[1,9)", "[3,9)", "[3,7)", "[1,7)"},
		{"nested_predicate", "boolean", "((a + b) * c) <@ c", "(((a + b) * c) <@ c)", "((a + b) * c) <@ c", "[1,3)", "[3,7)", "[2,4)", true},
		{"nested_comparison", "boolean", "((a + b) * c) = c", "(((a + b) * c) = c)", "((a + b) * c) = c", "[1,3)", "[3,7)", "[2,4)", true},
		{"qualified", "int4range", "(a OPERATOR(pg_catalog.+) b) OPERATOR(pg_catalog.*) c", "((a + b) * c)", "(a + b) * c", "[1,3)", "[3,7)", "[2,4)", "[2,4)"},
		{"multi_left_union", "int4multirange", "(a + b) * c", "((a + b) * c)", "(a + b) * c", "{[1,3)}", "{[3,7)}", "{[2,4)}", "{[2,4)}"},
		{"multi_right_difference", "int4multirange", "a - (b - c)", "(a - (b - c))", "a - (b - c)", "{[1,9)}", "{[3,9)}", "{[3,7)}", "{[1,7)}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				conn, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
				both := func(s fixture.Step) { run(s); run(fixture.Prepared(s)) }
				operand := tc.typ
				result := "BOOL"
				if tc.typ == "boolean" {
					operand = "int4range"
				} else {
					result = upperOperatorName(tc.typ)
				}
				run(fixture.Exec("CREATE TABLE range_deparse(k integer,a "+operand+",b "+operand+",c "+operand+",g "+tc.typ+" GENERATED ALWAYS AS ("+tc.expression+") STORED)", 0))
				run(fixture.Exec("INSERT INTO range_deparse(k,a,b,c) VALUES (1,"+rangeLiteral(tc.a)+","+rangeLiteral(tc.b)+","+rangeLiteral(tc.c)+"),(2,NULL,NULL,NULL)", 2))
				both(rangeSelect(backend, "SELECT g AS v FROM range_deparse ORDER BY k", []string{"v"}, []string{result}, [][]any{{tc.want}, {nil}}, []bool{true}))
				for _, mode := range []struct {
					pretty bool
					want   string
				}{{false, tc.compact}, {true, tc.pretty}} {
					q := fmt.Sprintf("SELECT pg_get_expr(d.adbin,d.adrelid,%t) AS definition FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class r ON d.adrelid=r.oid WHERE r.relname='range_deparse' AND d.adnum=5", mode.pretty)
					both(fixture.DeparseRelationsTextStep(t, backend, q, mode.want))
					definition := fixture.DeparseRelationsRead(t, p, backend, conn, q)
					both(rangeSelect(backend, "SELECT "+definition+" AS v FROM range_deparse ORDER BY k", []string{"v"}, []string{result}, [][]any{{tc.want}, {nil}}, nil))
				}
			}
		})
	}
}

func TestReferenceRangeOperatorIndexDeparse(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		both := func(s fixture.Step) { run(s); run(fixture.Prepared(s)) }
		run(fixture.Exec("CREATE TABLE range_index(k integer,a int4range,b int4range)", 0))
		run(fixture.Exec("INSERT INTO range_index VALUES (1,'[1,9)','[2,3)'),(2,'[1,3)','[2,8)'),(3,NULL,'[2,3)')", 3))
		// BOOL already has an index provider. No range-valued key is admitted here.
		run(fixture.Exec("CREATE INDEX range_predicate_idx ON range_index ((a @> b))", 0))
		for _, mode := range []struct {
			pretty        bool
			relation, key string
		}{
			{false, "public.range_index", "((a @> b))"},
			{true, "range_index", "(a @> b)"},
		} {
			full := "CREATE INDEX range_predicate_idx ON " + mode.relation + " USING btree (" + mode.key + ")"
			fullQuery := fmt.Sprintf("SELECT pg_get_indexdef('range_predicate_idx'::regclass,0,%t) AS definition", mode.pretty)
			keyQuery := fmt.Sprintf("SELECT pg_get_indexdef('range_predicate_idx'::regclass,1,%t) AS definition", mode.pretty)
			both(fixture.DeparseRelationsTextStep(t, backend, fullQuery, full))
			both(fixture.DeparseRelationsTextStep(t, backend, keyQuery, mode.key))
			key := fixture.DeparseRelationsRead(t, p, backend, conn, keyQuery)
			both(fixture.Select("SELECT "+key+" AS v FROM range_index ORDER BY k", []string{"v"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}))
			definition := fixture.DeparseRelationsRead(t, p, backend, conn, fullQuery)
			run(fixture.Exec("DROP INDEX range_predicate_idx", 0))
			run(fixture.Exec(definition, 0))
			both(fixture.DeparseRelationsTextStep(t, backend, fullQuery, full))
		}
	}
}
