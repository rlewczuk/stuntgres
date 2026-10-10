package reference_test

import (
	"fmt"
	"testing"
)

// All SQL scenarios use the existing shared fixture and the same body in both modes.
func primaryKeyReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func primaryKeyReferenceNull(table, column, row string) referenceDiagnostic {
	d := controlsReferenceDiagnostic("23502", fmt.Sprintf(`null value in column "%s" of relation "%s" violates not-null constraint`, column, table))
	d.Schema, d.Table, d.Column, d.Detail = "public", table, column, "Failing row contains ("+row+")."
	return d
}

func primaryKeyReferenceProtected(column string) referenceDiagnostic {
	return controlsReferenceDiagnostic("42P16", fmt.Sprintf(`column "%s" is in a primary key`, column))
}

func primaryKeyReferenceCatalog(table string, rows [][]any) referenceStep {
	return referenceSelect("SELECT c.conname,c.contype,pg_get_constraintdef(c.oid),c.conbin FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname=$1 ORDER BY c.conname", []string{"conname", "contype", "pg_get_constraintdef", "conbin"}, []string{"NAME", "CHAR", "TEXT", "PG_NODE_TREE"}, rows, table)
}

// Identities are runtime allocations, not release constants. Check real positive,
// distinct IDs and compare them across lifecycle transitions, never across backends.
func primaryKeyReferenceIDs(t *testing.T, p *referencePair, b referenceBackend, name string) [3]int64 {
	t.Helper()
	var ids [3]int64
	if err := b.db.QueryRowContext(p.ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname=$1 AND contype='p'", name).Scan(&ids[0], &ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relkind='i'", name).Scan(&ids[2]); err != nil {
		t.Fatal(err)
	}
	if ids[0] <= 0 || ids[1] <= 0 || ids[2] <= 0 || ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		t.Fatal("primary constraint, table and backing identities must be positive and distinct", ids)
	}
	return ids
}
