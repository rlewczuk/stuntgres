package reference_test

import "testing"

func TestReferenceUniqueIndexDeclaredTypes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TYPE ui_enum AS ENUM('first','second')", 0))
			run(referenceExec("CREATE DOMAIN ui_domain AS NUMERIC", 0))
			run(referenceExec("CREATE TABLE ui_t(a ui_enum,b ui_domain,p TEXT)", 0))
			run(referencePrepared(referenceExec("CREATE UNIQUE INDEX ui_ab ON ui_t(a enum_ops,b numeric_ops) INCLUDE(p)", 0)))
			run(referenceExec("INSERT INTO ui_t VALUES('first',1.0,'first'),('second',1.0,'second'),('first',2.0,'different')", 3))
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO ui_t VALUES('first',1.00,'payload')", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(first, 1.00) already exists."))))
			run(referenceExec("REINDEX INDEX ui_ab", 0))
			run(referenceExec("ALTER TABLE ui_t ALTER b TYPE NUMERIC", 0))
			run(referenceErrorDiagnostic("INSERT INTO ui_t VALUES('first',1.000,'payload')", uniqueReferenceDuplicate("ui_t", "ui_ab", "Key (a, b)=(first, 1.000) already exists.")))
			enumReferenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,b,p FROM ui_t ORDER BY a,b", []string{"a", "b", "p"}, []string{"", "NUMERIC", "TEXT"}, [][]any{{"first", "1.0", "first"}, {"first", "2.0", "different"}, {"second", "1.0", "second"}}))
		})
	}
}
