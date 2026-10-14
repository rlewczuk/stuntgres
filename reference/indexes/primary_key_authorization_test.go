package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pk_actor"})
	fixture.UniqueReferenceSchemaCleanup(t, "pk_guarded")
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE pk_actor", 0))
		run(fixture.Exec("CREATE SCHEMA pk_guarded", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public,pk_guarded TO pk_actor", 0))
		}
		run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'secret')", 1))
		query := "INSERT INTO t VALUES($1,$2)"
		held, err := a.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ex := fixture.HeldStatement{Executor: a, Statement: held, SQL: query}
		run(fixture.Exec("SET SESSION AUTHORIZATION pk_actor", 0))
		run(fixture.ImplicitError("CREATE TABLE pk_guarded.denied(a INT PRIMARY KEY)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pk_guarded", Position: "14"}))
		run(fixture.ImplicitError("INSERT INTO t VALUES(2,'x')", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
		fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, int64(2), "x"))
		for _, q := range []string{"ALTER TABLE t DROP CONSTRAINT t_pkey", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ALTER a TYPE BIGINT"} {
			s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "secret"}}))
		run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='denied' OR relname='denied_pkey'", []string{"relname"}, []string{"NAME"}, nil))
		// GRANT/REVOKE is a real PostgreSQL capability phase. Native all/partial/
		// no-key SELECT disclosure uses actual SetObjectACL package-function units.
		if b.PG {
			run(fixture.Exec("GRANT INSERT ON t TO pk_actor", 0))
			for _, mode := range []string{"none", "partial", "all", "table", "revoked"} {
				switch mode {
				case "partial":
					run(fixture.Exec("GRANT SELECT(a) ON t TO pk_actor", 0))
				case "all":
					run(fixture.Exec("GRANT SELECT(b) ON t TO pk_actor", 0))
				case "table":
					run(fixture.Exec("GRANT SELECT ON t TO pk_actor", 0))
				case "revoked":
					run(fixture.Exec("REVOKE SELECT ON t FROM pk_actor", 0))
					run(fixture.Exec("REVOKE SELECT(a,b) ON t FROM pk_actor", 0))
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION pk_actor", 0))
				want := fixture.UniqueReferenceDuplicate("t", "t_pkey", "")
				if mode == "all" || mode == "table" {
					want.Detail = "Key (a, b)=(1, secret) already exists."
				}
				fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.ErrorDiagnostic(query, want, int64(1), "secret"))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
	})
}

func TestReferencePrimaryKeyTypeBuildDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		t.Run(mode, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"pk_type_owner"})
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				defer a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE pk_type_owner", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION pk_type_owner", 0))
				fixture.WithPublicSchemaOwner(t, p, b, "pk_type_owner", func() { run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a,b))", 0)) })
				run(fixture.Exec("INSERT INTO t VALUES(1,'protected'),(2,'protected')", 2))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				before := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
				// Owner positive SQL and enforcement are shared. Native owner ACL
				// revocation is exercised through public SetObjectACL in package units;
				// SQL GRANT/REVOKE is intentionally only PostgreSQL here.
				if b.PG {
					run(fixture.Exec("REVOKE SELECT ON t FROM pk_type_owner", 0))
					if mode == "partial" || mode == "all" {
						run(fixture.Exec("GRANT SELECT(a) ON t TO pk_type_owner", 0))
					}
					if mode == "all" {
						run(fixture.Exec("GRANT SELECT(b) ON t TO pk_type_owner", 0))
					}
					if mode == "table" {
						run(fixture.Exec("GRANT SELECT ON t TO pk_type_owner", 0))
					}
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION pk_type_owner", 0))
				detail := "Key (a, b)=(0, protected) is duplicated."
				if b.PG && (mode == "none" || mode == "partial") {
					detail = "Duplicate keys exist."
				}
				want := fixture.UniqueReferenceDuplicate("t", "t_pkey", detail)
				want.Message = `could not create unique index "t_pkey"`
				s := fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ALTER a TYPE BIGINT USING 0", want))
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				if retained := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey"); retained != before {
					t.Fatal("failed owner TYPE changed identities", before, retained)
				}
				run(fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "protected"}, {int64(2), "protected"}}))
			})
		})
	}
}
