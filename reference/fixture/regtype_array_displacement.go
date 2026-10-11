package fixture

import (
	"database/sql"
	"strings"
	"testing"
)

// Exact names were independently observed on pinned PostgreSQL 18.0 in tests-17.
// REL_18_0 pg_type.c makeArrayTypeName and moveArrayTypeName explain why
// displacement seeds from the occupied ARRAY name before the new element's array.
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/pg_type.c
func RegtypeDisplacementScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn, *Recorder)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []Diagnostic
			_, conn, recorder := NoticeReferencePool(t, p, backend.PG, &notices)
			t.Cleanup(func() {
				if _, err := backend.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS regtype_displacement CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE SCHEMA regtype_displacement", "SET search_path TO regtype_displacement,public"} {
				RunStep(t, p.Ctx, conn, recorder, Exec(q, 0))
			}
			body(t, p, backend, conn, recorder)
		})
	}
}

func RegtypeDisplacementQualified(name string) string {
	if strings.HasSuffix(name, "[]") {
		return "regtype_displacement." + RegtypeIdentifier(strings.TrimSuffix(name, "[]")) + "[]"
	}
	return "regtype_displacement." + RegtypeIdentifier(name)
}

func RegtypeDisplacementOID(t *testing.T, p *Pair, conn *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := conn.QueryRowContext(p.Ctx, "SELECT $1::regtype::bigint", RegtypeDisplacementQualified(name)).Scan(&oid); err != nil {
		t.Fatal(name, err)
	}
	if oid <= 0 {
		t.Fatalf("nonpositive identity for %q: %d", name, oid)
	}
	return oid
}
