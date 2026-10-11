package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningUntypedAggregateBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'unchanged')", 1))
		for _, argument := range []string{"'7'", "NULL", "$1"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "UPDATE t SET id=2 RETURNING sum(" + argument + ")"
				position := 29
				if prefix != "" {
					position += 6
				}
				d := fixture.Diagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: strconv.Itoa(position)}
				for _, route := range []string{"exec", "query", "prepare"} {
					want := d
					if backend.PG && argument == "$1" && route != "prepare" {
						// No arguments uses PostgreSQL's simple protocol: the
						// missing parameter precedes aggregate overload inference.
						want = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(position + 4)}
					}
					s := fixture.ImplicitError(q, want)
					s.SelectRows = route == "query"
					s.Prepared = route == "prepare"
					s.ErrorStage = "execute"
					if s.Prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		}
		for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
			if strings.HasPrefix(q, "SELECT") {
				run(fixture.Select(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
			} else {
				message := "aggregate functions are not allowed in UPDATE"
				if strings.HasPrefix(q, "INSERT") {
					message = "aggregate functions are not allowed in VALUES"
				}
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42803", Message: message, Position: strconv.Itoa(strings.Index(q, "sum") + 1)}))
			}
		}
		run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
	}
}
