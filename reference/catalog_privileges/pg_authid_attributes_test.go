package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAuthIDAttributes(t *testing.T) {
	pgUserScenario(t, []string{"pga_attributes", "PgaÉ", "pga_renamed", "pga_parent"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgUserExec(t, p, b, "CREATE ROLE pga_attributes")
		var id int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pga_attributes'").Scan(&id); err != nil || id <= 0 {
			t.Fatal("role identity", err)
		}
		q := "SELECT * FROM pg_authid WHERE oid=$1"
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{pgAuthIDRow("pga_attributes", id, false)}, id))
		pgUserExec(t, p, b, "ALTER ROLE pga_attributes SUPERUSER NOINHERIT CREATEROLE CREATEDB LOGIN REPLICATION BYPASSRLS CONNECTION LIMIT 0")
		explicit := []any{id, "pga_attributes", true, false, true, true, true, true, true, int64(0), nil, nil}
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{explicit}, id))
		pgUserExec(t, p, b, "ALTER ROLE pga_attributes CONNECTION LIMIT 9")
		explicit[9] = int64(9)
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{explicit}, id))
		pgUserExec(t, p, b, "ALTER ROLE pga_attributes NOSUPERUSER INHERIT NOCREATEROLE NOCREATEDB NOLOGIN NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1")
		pgUserExec(t, p, b, "CREATE ROLE pga_parent SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS")
		pgUserExec(t, p, b, "GRANT pga_parent TO pga_attributes")
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{pgAuthIDRow("pga_attributes", id, false)}, id))
		pgUserExec(t, p, b, "CREATE ROLE \"PgaÉ\"")
		var mixed int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='PgaÉ'").Scan(&mixed); err != nil {
			t.Fatal(err)
		}
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{pgAuthIDRow("PgaÉ", mixed, false)}, mixed))
		pgAuthIDRoutes(t, p, b, fixture.Select("SELECT a.rolname,a.oid=r.oid AS same FROM pg_authid a JOIN pg_roles r ON r.oid=a.oid WHERE a.oid=$1", []string{"rolname", "same"}, []string{"NAME", "BOOL"}, [][]any{{"PgaÉ", true}}, mixed))
		liveName := `"PgaÉ"`
		if b.PG {
			pgUserExec(t, p, b, "ALTER ROLE \"PgaÉ\" RENAME TO pga_renamed")
			pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{pgAuthIDRow("pga_renamed", mixed, false)}, mixed))
			liveName = "pga_renamed"
		} else {
			pgAuthIDUnsupportedRename(t, p, b, `ALTER ROLE "PgaÉ" RENAME TO pga_renamed`)
			pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{pgAuthIDRow("PgaÉ", mixed, false)}, mixed))
		}
		pgUserExec(t, p, b, "DROP ROLE "+liveName)
		pgUserExec(t, p, b, "CREATE ROLE pga_renamed")
		var replacement int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pga_renamed'").Scan(&replacement); err != nil || replacement == mixed {
			t.Fatal("recreated identity", err)
		}
		pgAuthIDRoutes(t, p, b, pgAuthIDSelect(q, [][]any{}, mixed))
		for _, name := range []string{"pga_attributes", "pga_parent", "pga_renamed"} {
			pgUserExec(t, p, b, "DROP ROLE "+name)
		}
	})
}

// Role rename is outside role-core admission. Its native failure must preserve
// all catalog metadata; the oracle's rename-success phase remains explicit.
func pgAuthIDUnsupportedRename(t *testing.T, p *fixture.Pair, b fixture.Backend, q string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
