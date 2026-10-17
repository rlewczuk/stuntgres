package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func catalogRelationsBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}

func catalogRelationsRoleOID(t *testing.T, p *fixture.Pair, x fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing role", name, rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("role is not unique", name, rows.Err())
	}
	return oid
}

// PostgreSQL accessor expectations were observed on the pinned oracle in
// tests-2.md. Native uses the public typed-driver contract: string ScanType for
// NAME/REGCLASS/arrays/records and bool for BOOL. These computed/source columns
// conservatively expose unknown nullability (not an inferred NOT NULL claim).
// Selected descriptors carry no character length or decimal typmod.
func catalogRelationsMetadata(postgres bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range columns {
			scan := reflect.TypeFor[string]()
			switch c.DatabaseTypeName() {
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "NAME", "REGCLASS", "_TEXT", "RECORD":
				if postgres {
					scan = reflect.TypeFor[any]()
				}
			default:
				t.Fatalf("unexpected selected metadata type column=%d type=%q", i, c.DatabaseTypeName())
			}
			n, nk := c.Nullable()
			l, lk := c.Length()
			p, s, dk := c.DecimalSize()
			if c.ScanType() != scan || n || nk || l != 0 || lk || p != 0 || s != 0 || dk {
				t.Fatalf("metadata column=%d type=%q scan=%v want=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t",
					i, c.DatabaseTypeName(), c.ScanType(), scan, n, nk, l, lk, p, s, dk)
			}
		}
	}
}
