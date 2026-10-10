package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceExclusionParameterPhases(t *testing.T) {
	for _, suffix := range []string{"(($1) WITH =)", "(v WITH =) WHERE(v>$1)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", suffix, prepared), func(t *testing.T) {
				exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					c := concurrentIndexConn(t, p, b)
					run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
					run(referenceExec("CREATE TABLE t(v INT)", 0))
					q := "/*é*/ ALTER TABLE t ADD CONSTRAINT ex EXCLUDE" + suffix
					d := controlsReferenceDiagnostic("42P02", "there is no parameter $1")
					d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "$1")]) + 1)
					s := referenceErrorDiagnostic(q, d)
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
					run(referenceSelect("SELECT conname FROM pg_constraint WHERE conname='ex'", []string{"conname"}, []string{"NAME"}, nil))
					run(referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
				})
			})
		}
	}
}
