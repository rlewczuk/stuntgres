package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningDiagnostics(t *testing.T) {
	cases := []struct {
		q string
		d fixture.Diagnostic
	}{
		{"UPDATE t SET absent=1 WHERE missing_where=1 RETURNING missing_return", fixture.Diagnostic{Code: "42703", Message: `column "missing_where" does not exist`, Position: "29"}},
		{"UPDATE t SET absent=1 RETURNING missing_return", fixture.Diagnostic{Code: "42703", Message: `column "missing_return" does not exist`, Position: "33"}},
		{"UPDATE t SET id=CAST('bad' AS int) RETURNING hidden.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "hidden"`, Position: "46"}},
		{"UPDATE t SET id=$1 RETURNING $1", fixture.Diagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
		{"UPDATE t SET id=1 RETURNING $1,$1::int4", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o,OLD AS p) id", fixture.Diagnostic{Code: "42601", Message: "OLD cannot be specified multiple times", Position: "44"}},
		{"UPDATE t SET id=1 RETURNING WITH (NEW AS t) id", fixture.Diagnostic{Code: "42712", Message: `table name "t" specified more than once`, Position: "35"}},
		{"UPDATE t SET id=1 RETURNING WITH (OLD AS o) old.id", fixture.Diagnostic{Code: "42P01", Message: `missing FROM-clause entry for table "old"`, Position: "45"}},
		{"UPDATE t SET id=1 RETURNING sum(id)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
	}

	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
			if populated {
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(2,'unchanged')", 1))
			}
			for _, tc := range cases {
				for _, prefix := range []string{"", "/*é*/ "} {
					d := tc.d
					d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
					if prefix != "" {
						n, _ := strconv.Atoi(d.Position)
						d.Position = strconv.Itoa(n + 6)
					}
					p.Step(t, fixture.ImplicitError(prefix+tc.q, d, 2))
					queryError := fixture.ImplicitError(prefix+tc.q, d, 2)
					queryError.SelectRows = true
					p.Step(t, queryError)
					p.Step(t, fixture.Prepared(fixture.ImplicitError(prefix+tc.q, d, 2)))
				}
			}
			want := [][]any{}
			if populated {
				want = [][]any{{int64(2), "unchanged"}}
			}
			p.Step(t, fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, want))
		})
	}
}
