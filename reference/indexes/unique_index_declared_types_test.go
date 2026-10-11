package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueIndexDeclaredTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE ui_enum AS ENUM('first','second')", 0))
			run(fixture.Exec("CREATE DOMAIN ui_domain AS NUMERIC", 0))
			run(fixture.Exec("CREATE TABLE ui_t(a ui_enum,b ui_domain,p TEXT)", 0))
			run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX ui_ab ON ui_t(a enum_ops,b numeric_ops) INCLUDE(p)", 0)))
			run(fixture.Exec("INSERT INTO ui_t VALUES('first',1.0,'first'),('second',1.0,'second'),('first',2.0,'different')", 3))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES('first',1.00,'payload')", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(first, 1.00) already exists."))))
			run(fixture.Exec("REINDEX INDEX ui_ab", 0))
			run(fixture.Exec("ALTER TABLE ui_t ALTER b TYPE NUMERIC", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO ui_t VALUES('first',1.000,'payload')", fixture.UniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(first, 1.000) already exists.")))
			fixture.EnumReferenceRunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,b,p FROM ui_t ORDER BY a,b", []string{"a", "b", "p"}, []string{"", "NUMERIC", "TEXT"}, [][]any{{"first", "1.0", "first"}, {"first", "2.0", "different"}, {"second", "1.0", "second"}}))
		})
	}
}
