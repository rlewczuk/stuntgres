package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceAlterRenameTableTargets(t *testing.T) {
	for _, target := range []string{"t*", "ONLY t", "ONLY(t)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				p.step(t, referenceExec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
				p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
				s := referenceExec("ALTER TABLE "+target+" RENAME TO newtable", 0)
				s.prepared = prepared
				p.step(t, s)
				p.step(t, referenceSelect("SELECT * FROM newtable", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				failure := referenceDDLPositionError("SELECT * FROM t", "42P01", `relation "t" does not exist`, "15", false, "execute")
				failure.selectRows = true
				p.step(t, failure)
				q := "ALTER TABLE newtable RENAME TO t"
				if target == "t*" {
					q = "ALTER TABLE IF EXISTS newtable* RENAME TO t"
				}
				s = referenceExec(q, 0)
				s.prepared = prepared
				p.step(t, s)
				p.step(t, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
	}
}
