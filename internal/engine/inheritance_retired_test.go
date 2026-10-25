package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

// These are package fixtures: SQL constructs physical descriptors through the
// real providers; assertions exercise the issuer and catalog validator directly.
func retiredUnitTable(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
			return r
		}
	}
	t.Fatal("missing physical relation", name)
	return nil
}

func retiredRuntimeFixture(t *testing.T) (*core.Engine, *session.Operation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, q := range []string{
		"CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE q(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE c() INHERITS(p,q)",
		"CREATE TABLE leaf() INHERITS(c)",
	} {
		roleExecute(t, e, op, q)
	}
	return e, op
}
