package reference_test

import "testing"

func TestReferenceJSONCatalogAndGenericConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		// Native pg_type projections are outside the documented catalog surface.
		// Public array/domain consumers below witness the actual association;
		// descriptor OIDs and reverse links are covered by supplemental units.
		run(referenceExec("CREATE TABLE json_arrays(id INTEGER,v JSON[])", 0))
		array := `{NULL,"null"," {\"a\":1,\"a\":2} "}`
		for _, prepared := range []bool{false, true} {
			s := referenceExec("INSERT INTO json_arrays VALUES($1,$2)", 1, map[bool]int{false: 1, true: 2}[prepared], array)
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
		run(referenceExec("INSERT INTO json_arrays VALUES(3,NULL)", 1))
		run(referenceSelect("SELECT id,v FROM json_arrays ORDER BY id", []string{"id", "v"}, []string{"INT4", "_JSON"}, [][]any{{int64(1), array}, {int64(2), array}, {int64(3), nil}}))
		run(referenceExec("CREATE DOMAIN json_document AS JSON", 0))
		run(referenceExec("CREATE TABLE json_domains(id INTEGER,v json_document)", 0))
		run(referenceExec("INSERT INTO json_domains VALUES(1,$1),(2,'null'),(3,NULL)", 3, ` {"z":1,"z":2} `))
		run(referencePrepared(referenceSelect("SELECT id,v FROM json_domains ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":1,"z":2} `}, {int64(2), "null"}, {int64(3), nil}})))
		run(referencePrepared(referenceError("INSERT INTO json_domains VALUES(4,$1)", "22P02", "[1,2,]")))
		run(referenceSelect("SELECT id,v FROM json_domains ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(1), ` {"z":1,"z":2} `}, {int64(2), "null"}, {int64(3), nil}}))
	}
}
