//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Privileged PostgreSQL is a positive control, not native pg_catalog parity.
// Rollback restores the probe without needing system-object DROP authority.
func TestReferenceAlterSchemaPostgresSystemNamespace(t *testing.T) {
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "s1")
	r := &fixture.Recorder{}
	for _, q := range []string{"CREATE SCHEMA s1", "CREATE TABLE s1.alter_schema_system_probe(id INT)"} {
		fixture.RunStep(t, p.Ctx, p.Postgres, r, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, p.Postgres, r, fixture.Exec("INSERT INTO s1.alter_schema_system_probe VALUES(7)", 1))
	tx, err := p.Postgres.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.alter_schema_system_probe SET SCHEMA pg_catalog", 0)))
	fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("SELECT id FROM pg_catalog.alter_schema_system_probe", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Postgres, r, fixture.Select("SELECT id FROM s1.alter_schema_system_probe", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
