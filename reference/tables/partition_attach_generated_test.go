package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func partitionAttachGeneratedDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run("generated_mismatch/"+name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_gen_parent(k INT,payload INT) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_gen_child(k INT,payload INT GENERATED ALWAYS AS(k+1) STORED)", 0))
					run(fixture.Exec("INSERT INTO pa_gen_child(k) VALUES(1)", 1))
					s := fixture.ImplicitError("ALTER TABLE pa_gen_parent ATTACH PARTITION pa_gen_child FOR VALUES FROM(1) TO(3)", fixture.Diagnostic{Code: "42804", Message: `column "payload" in child table must not be a generated column`})
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT k,payload FROM pa_gen_child", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
					run(fixture.Select("SELECT k,payload FROM pa_gen_parent", []string{"k", "payload"}, []string{"INT4", "INT4"}, nil))
					run(fixture.Exec("INSERT INTO pa_gen_child(k) VALUES(9)", 1))
					run(fixture.Select("SELECT k,payload FROM pa_gen_child ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(9), int64(10)}}))
				})
			}
		})
	}
}
