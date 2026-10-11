//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectZeroWidth(t *testing.T) {
	p := fixture.NewPair(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT)", 0), fixture.Exec("INSERT INTO s VALUES(1),(1),(2)", 3), fixture.Exec("CREATE TABLE t(x INT)", 0), fixture.Exec("INSERT INTO t SELECT FROM s WHERE i=1", 2), fixture.Prepared(fixture.Exec("INSERT INTO t (SELECT)", 1))} {
		p.Step(t, s)
	}
	p.Step(t, fixture.Select("SELECT x FROM t", []string{"x"}, []string{"INT4"}, [][]any{{nil}, {nil}, {nil}}))
	for _, prepared := range []bool{false, true} {
		failure := fixture.ImplicitError("INSERT INTO t(x) SELECT", fixture.Diagnostic{Code: "42601", Message: "INSERT has more target columns than expressions", Position: "15"})
		failure.Prepared = prepared
		failure.ErrorStage = "execute"
		if prepared {
			failure.ErrorStage = "prepare"
		}
		p.Step(t, failure)
	}
	p.Step(t, fixture.Select("SELECT x FROM t", []string{"x"}, []string{"INT4"}, [][]any{{nil}, {nil}, {nil}}))
}
