package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachAtomicity(t *testing.T) {
	for _, branch := range []string{"candidate", "incumbent_default", "default_candidate"} {
		for _, nested := range []bool{false, true} {
			name := branch + "/regular"
			if nested {
				name = branch + "/nested"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE pa_atomic_parent(k INT,j INT) PARTITION BY RANGE(k)", 0))
						run(fixture.Exec("CREATE TABLE pa_atomic_sibling PARTITION OF pa_atomic_parent FOR VALUES FROM(20) TO(30)", 0))
						run(fixture.Exec("INSERT INTO pa_atomic_sibling VALUES(21,1)", 1))
						root := "pa_atomic_candidate"
						if branch == "incumbent_default" {
							root = "pa_atomic_default"
						}
						part := ""
						if nested {
							part = " PARTITION BY RANGE(j)"
						}
						run(fixture.Exec("CREATE TABLE "+root+"(k INT,j INT)"+part, 0))
						good, bad := root, root
						if nested {
							good, bad = root+"_good", root+"_bad"
							run(fixture.Exec("CREATE TABLE "+good+" PARTITION OF "+root+" FOR VALUES FROM(0) TO(10)", 0))
							run(fixture.Exec("CREATE TABLE "+bad+" PARTITION OF "+root+" FOR VALUES FROM(10) TO(20)", 0))
						}
						goodKey, badKey := int64(1), int64(9)
						if branch == "incumbent_default" {
							goodKey, badKey = 4, 2
						}
						if branch == "default_candidate" {
							goodKey, badKey = 4, 21
						}
						run(fixture.Exec("INSERT INTO "+good+" VALUES($1,1)", 1, goodKey))
						run(fixture.Exec("INSERT INTO "+bad+" VALUES($1,11)", 1, badKey))
						if branch == "incumbent_default" {
							run(fixture.Exec("ALTER TABLE pa_atomic_parent ATTACH PARTITION "+root+" DEFAULT", 0))
							run(fixture.Exec("CREATE TABLE pa_atomic_candidate(k INT,j INT)", 0))
						}
						q := "ALTER TABLE pa_atomic_parent ATTACH PARTITION pa_atomic_candidate FOR VALUES FROM(1) TO(3)"
						message := `partition constraint of relation "` + bad + `" is violated by some row`
						if branch == "incumbent_default" {
							message = `updated partition constraint for default partition "` + bad + `" would be violated by some row`
						}
						if branch == "default_candidate" {
							q = "ALTER TABLE pa_atomic_parent ATTACH PARTITION pa_atomic_candidate DEFAULT"
						}
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: message, Schema: "public", Table: bad})
							s.Prepared = prepared
							run(s)
						}
						rows := [][]any{{goodKey, int64(1)}, {badKey, int64(11)}}
						s := fixture.Select("SELECT k,j FROM "+root, []string{"k", "j"}, []string{"INT4", "INT4"}, rows)
						s.Bag = true
						run(s)
						run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_atomic_candidate'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
						parentRows := [][]any{{int64(21), int64(1)}}
						if branch == "incumbent_default" {
							parentRows = append(parentRows, rows...)
						}
						s = fixture.Select("SELECT k,j FROM pa_atomic_parent", []string{"k", "j"}, []string{"INT4", "INT4"}, parentRows)
						s.Bag = true
						run(s)
						run(fixture.Select("SELECT k,j FROM pa_atomic_sibling", []string{"k", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(21), int64(1)}}))
						switch branch {
						case "candidate":
							run(fixture.Exec("INSERT INTO "+root+" VALUES(8,2)", 1))
							run(fixture.Exec("ALTER TABLE pa_atomic_parent ATTACH PARTITION pa_atomic_candidate FOR VALUES FROM(1) TO(10)", 0))
						case "incumbent_default":
							run(fixture.Exec("ALTER TABLE pa_atomic_parent ATTACH PARTITION pa_atomic_candidate FOR VALUES FROM(5) TO(7)", 0))
							run(fixture.Exec("INSERT INTO pa_atomic_parent VALUES(5,1)", 1))
							run(fixture.Select("SELECT k,j FROM pa_atomic_candidate", []string{"k", "j"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(1)}}))
						case "default_candidate":
							run(fixture.Exec("INSERT INTO "+root+" VALUES(22,2)", 1))
							run(fixture.Exec("CREATE TABLE pa_atomic_other(k INT,j INT) PARTITION BY RANGE(k)", 0))
							run(fixture.Exec("ALTER TABLE pa_atomic_other ATTACH PARTITION pa_atomic_candidate DEFAULT", 0))
						}
					})
				}
			})
		}
	}
}
