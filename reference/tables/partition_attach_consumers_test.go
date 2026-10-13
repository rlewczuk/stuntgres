package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func partitionAttachMappedConsumers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run("mapped_consumers/"+name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_consumer(k INT,payload TEXT,UNIQUE(k)) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_consumer_child(payload TEXT,k INT)", 0))
					run(fixture.Exec("ALTER TABLE pa_consumer ATTACH PARTITION pa_consumer_child FOR VALUES FROM(1) TO(5)", 0))
					run(fixture.Select("INSERT INTO pa_consumer VALUES(1,'one'),(2,'two') RETURNING k,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
					run(fixture.Select("INSERT INTO pa_consumer SELECT 3,'three' RETURNING k,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "three"}}))
					run(fixture.Exec("INSERT INTO pa_consumer SELECT 4,'four'", 1))
					run(fixture.Exec("CREATE VIEW pa_consumer_view AS SELECT k,payload FROM pa_consumer", 0))
					want := [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}, {int64(4), "four"}}
					run(fixture.Select("SELECT * FROM pa_consumer_view ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, want))
					run(fixture.Exec("CREATE TABLE pa_consumer_copy AS SELECT k,payload FROM pa_consumer", 4))
					run(fixture.Select("SELECT * FROM pa_consumer_copy ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, want))
					run(fixture.Select("SELECT * FROM pa_consumer_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"one", int64(1)}, {"two", int64(2)}, {"three", int64(3)}, {"four", int64(4)}}))
					physical := fixture.Select("SELECT tableoid='pa_consumer_child'::regclass::OID AS physical FROM pa_consumer ORDER BY k", []string{"physical"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}})
					if !b.PG {
						physical = fixture.ImplicitError(physical.Query, fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"})
					}
					run(physical)
				})
			}
		})
	}
}
