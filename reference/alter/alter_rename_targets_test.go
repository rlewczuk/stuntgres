package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterRenameTableTargets(t *testing.T) {
	for _, target := range []string{"t*", "ONLY t", "ONLY(t)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+fmt.Sprint(prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
				s := fixture.Exec("ALTER TABLE "+target+" RENAME TO newtable", 0)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, fixture.Select("SELECT * FROM newtable", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				failure := referenceDDLPositionError("SELECT * FROM t", "42P01", `relation "t" does not exist`, "15", false, "execute")
				failure.SelectRows = true
				p.Step(t, failure)
				q := "ALTER TABLE newtable RENAME TO t"
				if target == "t*" {
					q = "ALTER TABLE IF EXISTS newtable* RENAME TO t"
				}
				s = fixture.Exec(q, 0)
				s.Prepared = prepared
				p.Step(t, s)
				p.Step(t, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
	}
}
