//go:build reference

package reference_test

import "testing"

// Privileged PostgreSQL is a positive control, not native pg_catalog parity.
// Rollback restores the probe without needing system-object DROP authority.
func TestReferenceAlterSchemaPostgresSystemNamespace(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1")
	r := &referenceRecorder{}
	for _, q := range []string{"CREATE SCHEMA s1", "CREATE TABLE s1.alter_schema_system_probe(id INT)"} {
		referenceRunStep(t, p.ctx, p.postgres, r, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, p.postgres, r, referenceExec("INSERT INTO s1.alter_schema_system_probe VALUES(7)", 1))
	tx, err := p.postgres.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRunStep(t, p.ctx, tx, r, referencePrepared(referenceExec("ALTER TABLE s1.alter_schema_system_probe SET SCHEMA pg_catalog", 0)))
	referenceRunStep(t, p.ctx, tx, r, referenceSelect("SELECT id FROM pg_catalog.alter_schema_system_probe", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.postgres, r, referenceSelect("SELECT id FROM s1.alter_schema_system_probe", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
