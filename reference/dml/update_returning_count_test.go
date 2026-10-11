package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningCountCoexistence(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(NULL),(2)", 3))
	counts := fixture.Select("SELECT count(*),count(id),count(NULL) FROM t", []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(0)}})
	p.Step(t, counts)
	p.Step(t, fixture.Prepared(counts))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		output string
		d      fixture.Diagnostic
	}{
		{"count(*)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(NULL)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count()", fixture.Diagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function"}},
		{"count(id,id)", fixture.Diagnostic{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint}},
		{"count(zzzzz)", fixture.Diagnostic{Code: "42703", Message: `column "zzzzz" does not exist`}},
	} {
		for _, command := range []struct {
			sql      string
			position int
		}{{"UPDATE t SET id=2 RETURNING ", 29}, {"DELETE FROM t RETURNING ", 25}} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + command.sql + tc.output
				d := tc.d
				position := command.position
				if tc.output == "count(zzzzz)" {
					position += 6
				}
				if prefix != "" {
					position += 6
				}
				d.Position = strconv.Itoa(position)
				p.Step(t, fixture.ImplicitError(q, d))
				queryError := fixture.ImplicitError(q, d)
				queryError.SelectRows = true
				p.Step(t, queryError)
				p.Step(t, fixture.Prepared(fixture.ImplicitError(q, d)))
			}
		}
	}
	final := fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}, {int64(2)}})
	final.Bag = true
	p.Step(t, final)
}
