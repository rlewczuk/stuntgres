package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCatalogAndGenericConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		// Native pg_type projections are outside the documented catalog surface.
		// Public array/domain consumers below witness the actual association;
		// descriptor OIDs and reverse links are covered by supplemental units.
		run(fixture.Exec("CREATE TABLE json_arrays(id INTEGER,v JSON[])", 0))
		array := `{NULL,"null"," {\"a\":1,\"a\":2} "}`
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO json_arrays VALUES($1,$2)", 1, map[bool]int{false: 1, true: 2}[prepared], array)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		run(fixture.Exec("INSERT INTO json_arrays VALUES(3,NULL)", 1))
		run(fixture.Select("SELECT id,v FROM json_arrays ORDER BY id", []string{"id", "v"}, []string{"INT4", "_JSON"}, [][]any{{int64(1), array}, {int64(2), array}, {int64(3), nil}}))
		run(fixture.Exec("CREATE DOMAIN json_document AS JSON", 0))
		run(fixture.Exec("CREATE TABLE json_domains(id INTEGER,v json_document)", 0))
		run(fixture.Exec("INSERT INTO json_domains VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"z":1,"z":2} `))
		run(fixture.Prepared(fixture.Select("SELECT id,v FROM json_domains ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":1,"z":2} `}, {int64(2), "null"}, {int64(3), nil}})))
		run(fixture.Prepared(fixture.Error("INSERT INTO json_domains VALUES(4,$1)", "22P02", "[1,2,]")))
		run(fixture.Select("SELECT id,v FROM json_domains ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":1,"z":2} `}, {int64(2), "null"}, {int64(3), nil}}))
	}
}
