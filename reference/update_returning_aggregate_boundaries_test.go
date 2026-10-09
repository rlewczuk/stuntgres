package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceUpdateReturningUntypedAggregateBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT,v TEXT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'unchanged')", 1))
		for _, argument := range []string{"'7'", "NULL", "$1"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "UPDATE t SET id=2 RETURNING sum(" + argument + ")"
				position := 29
				if prefix != "" {
					position += 6
				}
				d := referenceDiagnostic{Code: "0A000", Message: "untyped aggregate arguments are unsupported", Position: strconv.Itoa(position)}
				if backend.pg {
					d = referenceDiagnostic{Code: "42725", Message: "function sum(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: strconv.Itoa(position)}
				}
				for _, route := range []string{"exec", "query", "prepare"} {
					want := d
					if backend.pg && argument == "$1" && route != "prepare" {
						// No arguments uses PostgreSQL's simple protocol: the
						// missing parameter precedes aggregate overload inference.
						want = referenceDiagnostic{Code: "42P02", Message: "there is no parameter $1", Position: strconv.Itoa(position + 4)}
					}
					s := referenceImplicitError(q, want)
					s.selectRows = route == "query"
					s.prepared = route == "prepare"
					s.errorStage = "execute"
					if s.prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
		}
		for _, q := range []string{"SELECT sum(id) FROM t", "UPDATE t SET id=sum(id)", "INSERT INTO t(id) VALUES(sum(1))"} {
			if !backend.pg {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, "sum") + 1)}))
			} else if strings.HasPrefix(q, "SELECT") {
				run(referenceSelect(q, []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
			} else {
				message := "aggregate functions are not allowed in UPDATE"
				if strings.HasPrefix(q, "INSERT") {
					message = "aggregate functions are not allowed in VALUES"
				}
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42803", Message: message, Position: strconv.Itoa(strings.Index(q, "sum") + 1)}))
			}
		}
		run(referenceSelect("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "unchanged"}}))
	}
}
