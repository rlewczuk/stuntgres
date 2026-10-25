package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachSchema(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := "reordered"
			if sparse {
				name = "sparse"
			}
			if prepared {
				name += "/prepared"
			} else {
				name += "/direct"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE pa_schema_parent(k INT NOT NULL,payload TEXT DEFAULT 'parent',CONSTRAINT pa_schema_check CHECK(k>0)) PARTITION BY RANGE(k)", 0))
						extra := ""
						if sparse {
							extra = "obsolete INT,"
						}
						run(fixture.Exec("CREATE TABLE pa_schema_child("+extra+"payload TEXT NOT NULL DEFAULT 'child',k INT NOT NULL DEFAULT 2,CONSTRAINT pa_schema_check CHECK(k>0),CONSTRAINT pa_schema_local CHECK(length(payload)>0),CONSTRAINT pa_schema_key UNIQUE(k))", 0))
						if sparse {
							run(fixture.Exec("ALTER TABLE pa_schema_child DROP COLUMN obsolete", 0))
						}
						run(fixture.Exec("INSERT INTO pa_schema_child(payload,k) VALUES('one',1)", 1))
						run(fixture.Exec("ALTER TABLE pa_schema_parent ATTACH PARTITION pa_schema_child FOR VALUES FROM(1) TO(5)", 0))
						run(fixture.Select("SELECT * FROM pa_schema_parent", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
						child := fixture.Select("SELECT * FROM pa_schema_child", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}})
						child.CheckMetadata = referenceRangeMetadata(b.PG, child.Columns, child.Types, []bool{false, false})
						run(child)
						run(fixture.Exec("INSERT INTO pa_schema_parent(k) VALUES(3)", 1))
						run(fixture.Exec("INSERT INTO pa_schema_child(payload) VALUES('own')", 1))
						run(fixture.Select("SELECT payload,k FROM pa_schema_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"own", int64(2)}, {"parent", int64(3)}}))
						run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pa_schema_child'::regclass::OID AND contype='c' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"pa_schema_check"}, {"pa_schema_local"}}))
						run(fixture.ImplicitError("INSERT INTO pa_schema_child VALUES('',4)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pa_schema_child" violates check constraint "pa_schema_local"`, Detail: "Failing row contains (, 4).", Schema: "public", Table: "pa_schema_child", Constraint: "pa_schema_local"}))
						run(fixture.ImplicitError("INSERT INTO pa_schema_parent VALUES(4,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "payload" of relation "pa_schema_child" violates not-null constraint`, Detail: "Failing row contains (4, null).", Schema: "public", Table: "pa_schema_child", Column: "payload"}))
						run(fixture.ImplicitError("INSERT INTO pa_schema_child VALUES(NULL,4)", fixture.Diagnostic{Code: "23502", Message: `null value in column "payload" of relation "pa_schema_child" violates not-null constraint`, Detail: "Failing row contains (null, 4).", Schema: "public", Table: "pa_schema_child", Column: "payload"}))
						run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_schema_child'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{true}}))
					})
				}
			})
		}
	}
	partitionAttachConstraintProvenance(t)
}
