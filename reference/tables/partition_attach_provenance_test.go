package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func partitionAttachConstraintProvenance(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "regular"
		if nested {
			name = "nested"
		}
		t.Run("constraint_provenance/"+name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_origin_parent(k INT CONSTRAINT pa_origin_parent_nn NOT NULL,payload TEXT,CONSTRAINT pa_origin_shared CHECK(k>0)) PARTITION BY RANGE(k)", 0))
					part := ""
					if nested {
						part = " PARTITION BY RANGE(payload)"
					}
					run(fixture.Exec("CREATE TABLE pa_origin_child(payload TEXT,k INT CONSTRAINT pa_origin_child_nn NOT NULL,CONSTRAINT pa_origin_shared CHECK(k>0),CONSTRAINT pa_origin_local CHECK(length(payload)>0))"+part, 0))
					if nested {
						run(fixture.Exec("CREATE TABLE pa_origin_leaf PARTITION OF pa_origin_child DEFAULT", 0))
					}
					run(fixture.Exec("INSERT INTO pa_origin_child VALUES('one',1)", 1))
					run(fixture.Select("SELECT conname,contype,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pa_origin_child'::regclass::OID ORDER BY conname", []string{"conname", "contype", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "INT2"}, [][]any{{"pa_origin_child_nn", "n", true, int64(0)}, {"pa_origin_local", "c", true, int64(0)}, {"pa_origin_shared", "c", true, int64(0)}}))
					run(fixture.Exec("ALTER TABLE pa_origin_parent ATTACH PARTITION pa_origin_child FOR VALUES FROM(1) TO(3)", 0))
					check := func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
						fixture.RunStep(t, p.Ctx, x, r, fixture.Select("SELECT conname,contype,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pa_origin_child'::regclass::OID ORDER BY conname", []string{"conname", "contype", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "INT2"}, [][]any{{"pa_origin_child_nn", "n", false, int64(1)}, {"pa_origin_local", "c", true, int64(0)}, {"pa_origin_shared", "c", false, int64(1)}}))
						fixture.RunStep(t, p.Ctx, x, r, fixture.Select("SELECT * FROM pa_origin_parent", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
						fixture.RunStep(t, p.Ctx, x, r, fixture.Select("SELECT * FROM pa_origin_child", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}}))
					}
					check(t, p, b.DB, b.Recorder, b.PG)
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						check(t, p, b.DB, b.Recorder, false)
						if p.Postgres != nil {
							partitionAttachReplay(t, p, dump.String(), check)
						}
					}
				})
			}
		})
	}
}
