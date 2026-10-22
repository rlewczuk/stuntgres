package reference_test

import (
	"database/sql"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferencePgrowlocksNamesPermissionsAndDiagnostics(t *testing.T) {
	t.Run("names-and-kinds", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "Pgrow Names")
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			for _, q := range []string{
				`CREATE SCHEMA "Pgrow Names"`, `CREATE TABLE "Pgrow Names"."Case Table"(id INT)`,
				`CREATE TABLE "98765"(id INT)`, `CREATE TABLE pgrow_child() INHERITS(pgrow_t)`,
				`INSERT INTO pgrow_child VALUES(2,0)`,
				`CREATE VIEW pgrow_view AS SELECT * FROM pgrow_t`,
				`CREATE MATERIALIZED VIEW pgrow_mv AS SELECT * FROM pgrow_t`,
				`CREATE SEQUENCE pgrow_seq`, `CREATE INDEX pgrow_index ON pgrow_t(v)`,
				`CREATE TABLE pgrow_part(id INT) PARTITION BY RANGE(id)`,
				`CREATE FOREIGN DATA WRAPPER pgrow_fdw`, `CREATE SERVER pgrow_server FOREIGN DATA WRAPPER pgrow_fdw`,
				`CREATE FOREIGN TABLE pgrow_foreign(id INT) SERVER pgrow_server`,
			} {
				if _, err := a.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			t.Cleanup(func() {
				a.ExecContext(p.Ctx, "DROP SERVER IF EXISTS pgrow_server CASCADE")
				a.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS pgrow_fdw CASCADE")
			})
			for _, name := range []string{`"Pgrow Names"."Case Table"`, "98765", "public.pgrow_t"} {
				if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks($1::text)", name); len(got) != 0 {
					t.Fatal(name, got)
				}
			}
			var db string
			if err := observer.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
				t.Fatal(err)
			}
			if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks($1::text)", pq.QuoteIdentifier(db)+".public.pgrow_t"); len(got) != 0 {
				t.Fatal(got)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			identityLock(t, p, a, "pgrow_child", 2, "UPDATE")
			pgrowEmpty(t, p, observer)
			pgrowOne(t, pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_child')"), false, pgrowOwner(t, p, a, "For Update"))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			for _, table := range []string{`"Pgrow Names"."Case Table"`, `"98765"`} {
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+table+" VALUES(1)", 1)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				owner := pgrowOwner(t, p, a, "For Update")
				identityLock(t, p, a, table, 1, "UPDATE")
				pgrowOne(t, pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks($1::text)", table), false, owner)
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner := pgrowOwner(t, p, a, "For Share")
			identityLock(t, p, a, "pgrow_t", 1, "SHARE")
			pgrowOne(t, pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks($1::text)", pq.QuoteIdentifier(db)+".public.pgrow_t"), false, owner)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, observer, "CREATE TEMP TABLE pgrow_temp(id INT)", 0)
			if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks('pg_temp.pgrow_temp')"); len(got) != 0 {
				t.Fatal(got)
			}
			fixture.LockExec(t, p.Ctx, observer, "INSERT INTO pgrow_temp VALUES(1)", 1)
			fixture.LockExec(t, p.Ctx, observer, "BEGIN", 0)
			tempOwner := pgrowOwner(t, p, observer, "For Share")
			identityLock(t, p, observer, "pgrow_temp", 1, "SHARE")
			pgrowOne(t, pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks('pg_temp.pgrow_temp')"), false, tempOwner)
			fixture.LockExec(t, p.Ctx, observer, "ROLLBACK", 0)
			for _, tc := range []struct{ name, code, message, detail string }{
				{"pgrow_missing", "42P01", `relation "pgrow_missing" does not exist`, ""},
				{"pgrow_absent_schema.t", "3F000", `schema "pgrow_absent_schema" does not exist`, ""},
				{"", "42602", "invalid name syntax", ""},
				{"a..b", "42602", "invalid name syntax", ""},
				{"a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d", ""},
				{"pgrow_other_database.public.pgrow_t", "0A000", `cross-database references are not implemented: "pgrow_other_database.public.pgrow_t"`, ""},
				{"pgrow_view", "42809", `"pgrow_view" is not a table`, ""},
				{"pgrow_mv", "42809", `"pgrow_mv" is not a table`, ""},
				{"pgrow_seq", "42809", `"pgrow_seq" is not a table`, ""},
				{"pgrow_index", "42809", `"pgrow_index" is not a table`, ""},
				{"pgrow_foreign", "42809", `"pgrow_foreign" is not a table`, ""},
				{"pgrow_part", "42809", `"pgrow_part" is a partitioned table`, "Partitioned tables do not contain rows."},
			} {
				t.Run(tc.name, func(t *testing.T) {
					pgrowError(t, p, observer, "SELECT * FROM pgrowlocks($1::text)", "execute", fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail}, tc.name)
				})
			}
			// Only PostgreSQL exposes genuine custom-table-AM setup. Native
			// represented ordinary storage is heap, with no AM descriptor to forge.
			if b.PG {
				if _, err := a.ExecContext(p.Ctx, "CREATE ACCESS METHOD pgrow_nonheap TYPE TABLE HANDLER heap_tableam_handler"); err != nil {
					t.Fatal(err)
				}
				defer a.ExecContext(p.Ctx, "DROP ACCESS METHOD pgrow_nonheap CASCADE")
				if _, err := a.ExecContext(p.Ctx, "CREATE TABLE pgrow_nonheap_t(id INT) USING pgrow_nonheap"); err != nil {
					t.Fatal(err)
				}
				pgrowError(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_nonheap_t')", "execute", fixture.Diagnostic{Code: "0A000", Message: "only heap AM is supported"})
			}
			fixture.LockExec(t, p.Ctx, observer, "BEGIN", 0)
			_, err := observer.ExecContext(p.Ctx, "SELECT * FROM pgrowlocks('pgrow_missing')")
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "pgrow_missing" does not exist`)
			fixture.AssertDiagnostic(t, err, &want)
			_, err = observer.ExecContext(p.Ctx, "SELECT 1")
			aborted := fixture.TransactionAborted()
			fixture.AssertDiagnostic(t, err, &aborted)
			fixture.LockExec(t, p.Ctx, observer, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
		})
	})
	t.Run("privileges-and-unfiltered-storage", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pgrow_reader", "pgrow_noinherit", "pgrow_group"})
		fixture.SchemaReferenceCleanup(t, "pgrow_private")
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			for _, q := range []string{
				"CREATE ROLE pgrow_reader", "CREATE ROLE pgrow_noinherit NOINHERIT", "CREATE ROLE pgrow_group",
				"GRANT USAGE ON SCHEMA public TO pgrow_reader,pgrow_noinherit",
				"GRANT pg_stat_scan_tables TO pgrow_group", "GRANT pgrow_group TO pgrow_reader,pgrow_noinherit",
				"CREATE SCHEMA pgrow_private", "CREATE TABLE pgrow_private.hidden(id INT)",
				"ALTER TABLE pgrow_t ENABLE ROW LEVEL SECURITY", "CREATE POLICY pgrow_none ON pgrow_t USING(false)",
				"CREATE VIEW pgrow_denied_view AS SELECT * FROM pgrow_t",
			} {
				if _, err := a.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner := pgrowOwner(t, p, a, "For Update")
			identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_reader", 0)
			pgrowOne(t, pgrowGet(t, p, observer), false, owner)
			pgrowError(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_private.hidden')", "execute", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgrow_private"})
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_noinherit", 0)
			pgrowError(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_t')", "execute", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pgrow_t"})
			// Kind must win over table SELECT denial.
			pgrowError(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_denied_view')", "execute", fixture.Diagnostic{Code: "42809", Message: `"pgrow_denied_view" is not a table`})
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
			// Grants on the relation need AX; release the holder before ACL DDL.
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, a, "REVOKE pgrow_group FROM pgrow_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "GRANT SELECT(id) ON pgrow_t TO pgrow_reader", 0)
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_reader", 0)
			pgrowError(t, p, observer, "SELECT * FROM pgrowlocks('pgrow_t')", "execute", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pgrow_t"})
			if got := pgrowQuery(t, p, observer, "SELECT * FROM pgrowlocks(NULL::text)"); len(got) != 0 {
				t.Fatal(got)
			}
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
			fixture.LockExec(t, p.Ctx, a, "GRANT SELECT ON pgrow_t TO pgrow_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner = pgrowOwner(t, p, a, "For Update")
			identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_reader", 0)
			if b.PG {
				identityBoth(t, p, b, observer, fixture.Select("SELECT id FROM pgrow_t", []string{"id"}, []string{"INT4"}, nil))
			} else {
				// Ordinary policy enforcement is an existing native capability
				// boundary, not a pgrowlocks storage-scan restriction.
				pgrowError(t, p, observer, "SELECT id FROM pgrow_t", "execute", fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"})
			}
			pgrowOne(t, pgrowGet(t, p, observer), false, owner)
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			stmt, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pgrowlocks($1::text)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.LockExec(t, p.Ctx, a, "REVOKE EXECUTE ON FUNCTION pgrowlocks(text) FROM PUBLIC", 0)
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_reader", 0)
			pgrowError(t, p, observer, "SELECT * FROM pgrowlocks(NULL::text)", "execute", fixture.Diagnostic{Code: "42501", Message: "permission denied for function pgrowlocks"})
			rows, err := stmt.QueryContext(p.Ctx, nil)
			if rows != nil {
				rows.Close()
				t.Fatal("denied held query returned rows")
			}
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pgrowlocks")
			fixture.AssertDiagnostic(t, err, &want)
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
			fixture.LockExec(t, p.Ctx, a, "GRANT EXECUTE ON FUNCTION pgrowlocks(text) TO pgrow_reader", 0)
			fixture.LockExec(t, p.Ctx, observer, "SET ROLE pgrow_reader", 0)
			rows, err = stmt.QueryContext(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := pgrowRead(t, rows); len(got) != 0 {
				t.Fatal(got)
			}
			fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
		})
	})
}
