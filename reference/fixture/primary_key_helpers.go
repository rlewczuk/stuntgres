package fixture

import (
	"fmt"
	"testing"
)

// All SQL scenarios use the existing shared fixture and the same body in both modes.
func PrimaryKeyReferenceScenario(t *testing.T, body func(*testing.T, *Pair, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func PrimaryKeyReferenceNull(table, column, row string) Diagnostic {
	d := ControlsReferenceDiagnostic("23502", fmt.Sprintf(`null value in column "%s" of relation "%s" violates not-null constraint`, column, table))
	d.Schema, d.Table, d.Column, d.Detail = "public", table, column, "Failing row contains ("+row+")."
	return d
}

func PrimaryKeyReferenceProtected(column string) Diagnostic {
	return ControlsReferenceDiagnostic("42P16", fmt.Sprintf(`column "%s" is in a primary key`, column))
}

func PrimaryKeyReferenceCatalog(table string, rows [][]any) Step {
	return Select("SELECT c.conname,c.contype,pg_get_constraintdef(c.oid),c.conbin FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relname=$1 ORDER BY c.conname", []string{"conname", "contype", "pg_get_constraintdef", "conbin"}, []string{"NAME", "CHAR", "TEXT", "PG_NODE_TREE"}, rows, table)
}

// Identities are runtime allocations, not release constants. Check real positive,
// distinct IDs and compare them across lifecycle transitions, never across backends.
func PrimaryKeyReferenceIDs(t *testing.T, p *Pair, b Backend, name string) [3]int64 {
	t.Helper()
	var ids [3]int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname=$1 AND contype='p'", name).Scan(&ids[0], &ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relkind='i'", name).Scan(&ids[2]); err != nil {
		t.Fatal(err)
	}
	if ids[0] <= 0 || ids[1] <= 0 || ids[2] <= 0 || ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		t.Fatal("primary constraint, table and backing identities must be positive and distinct", ids)
	}
	return ids
}
