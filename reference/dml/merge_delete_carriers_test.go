package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Pinned PostgreSQL preserves the MERGE-local carrier truth contract with both
// missing-side families, including constant/parameter base/domain carriers.
func TestReferenceMergeDeleteFullJoinCarriers(t *testing.T) {
	for _, tc := range []struct {
		name, typ, literal string
		argument           any
		matches            bool
	}{
		{"boolTrue", "BOOLEAN", "TRUE", true, true},
		{"boolFalse", "BOOLEAN", "FALSE", false, false},
		{"boolNull", "BOOLEAN", "NULL", nil, false},
		{"int2Zero", "SMALLINT", "0", int64(0), false},
		{"int2Negative", "SMALLINT", "-32768", int64(-32768), true},
		{"int4Zero", "INTEGER", "0", int64(0), false},
		{"int4Negative", "INTEGER", "-1", int64(-1), true},
		{"int4Max", "INTEGER", "2147483647", int64(2147483647), true},
		{"int4Null", "INTEGER", "NULL", nil, false},
		{"int8Wide", "BIGINT", "4294967296", int64(4294967296), true},
		{"int8Negative", "BIGINT", "-9223372036854775808", int64(-9223372036854775808), true},
		{"int8Null", "BIGINT", "NULL", nil, false},
		{"textEmpty", "TEXT", "''", "", true},
		{"textZero", "TEXT", "'0'", "0", true},
		{"textFalse", "TEXT", "'false'", "false", true},
		{"textNull", "TEXT", "NULL", nil, false},
	} {
		for _, domain := range []bool{false, true} {
			for _, parameter := range []bool{false, true} {
				t.Run(tc.name+map[bool]string{false: "/base", true: "/domain"}[domain]+map[bool]string{false: "/constant", true: "/parameter"}[parameter], func(t *testing.T) {
					mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
						run := fixture.MergeRun(t, p, b, prepared)
						typ := tc.typ
						if domain {
							run(fixture.Exec("CREATE DOMAIN carrier AS "+typ, 0))
							typ = "carrier"
						}
						run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
						expression := "(" + tc.literal + ")::" + typ
						if parameter {
							expression = "$1::" + typ
						}
						q := "MERGE INTO t USING (VALUES(2)) s(id) ON " + expression + " WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET THEN INSERT VALUES(s.id,40)"
						n := int64(1)
						var want [][]any
						if !tc.matches {
							n = 2
							want = [][]any{{int64(2), int64(40)}}
						}
						step := fixture.Exec(q, n)
						if parameter {
							step.Args = []any{tc.argument}
						}
						run(step)
						run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
					})
				})
			}
		}
	}
}
