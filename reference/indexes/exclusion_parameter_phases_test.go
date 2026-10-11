package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceExclusionParameterPhases(t *testing.T) {
	for _, suffix := range []string{"(($1) WITH =)", "(v WITH =) WHERE(v>$1)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", suffix, prepared), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := fixture.ConcurrentIndexConn(t, p, b)
					run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
					run(fixture.Exec("CREATE TABLE t(v INT)", 0))
					q := "/*é*/ ALTER TABLE t ADD CONSTRAINT ex EXCLUDE" + suffix
					d := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
					d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "$1")]) + 1)
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ex'", []string{"conname"}, []string{"NAME"}, nil))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
				})
			})
		}
	}
}
