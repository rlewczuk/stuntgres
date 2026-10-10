package reference_test

import "testing"

func TestReferencePrimaryKeyAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"pk_actor"})
	uniqueReferenceSchemaCleanup(t, "pk_guarded")
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		defer a.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("CREATE ROLE pk_actor", 0))
		run(referenceExec("CREATE SCHEMA pk_guarded", 0))
		if b.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public,pk_guarded TO pk_actor", 0))
		}
		run(referenceExec("CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'secret')", 1))
		query := "INSERT INTO t VALUES($1,$2)"
		held, err := a.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ex := referenceHeldStatement{referenceExecutor: a, statement: held, query: query}
		run(referenceExec("SET SESSION AUTHORIZATION pk_actor", 0))
		run(referenceImplicitError("CREATE TABLE pk_guarded.denied(a INT PRIMARY KEY)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema pk_guarded", Position: "14"}))
		run(referenceImplicitError("INSERT INTO t VALUES(2,'x')", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}))
		referenceRunStep(t, p.ctx, ex, b.recorder, referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}, int64(2), "x"))
		for _, q := range []string{"ALTER TABLE t DROP CONSTRAINT t_pkey", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ALTER a TYPE BIGINT"} {
			s := referencePrepared(referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "must be owner of table t"}))
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "secret"}}))
		run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_b_not_null", "n", "NOT NULL b", nil}, {"t_pkey", "p", "PRIMARY KEY (a, b)", nil}}))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='denied' OR relname='denied_pkey'", []string{"relname"}, []string{"NAME"}, nil))
		// GRANT/REVOKE is a real PostgreSQL capability phase. Native all/partial/
		// no-key SELECT disclosure uses actual SetObjectACL package-function units.
		if b.pg {
			run(referenceExec("GRANT INSERT ON t TO pk_actor", 0))
			for _, mode := range []string{"none", "partial", "all", "table", "revoked"} {
				switch mode {
				case "partial":
					run(referenceExec("GRANT SELECT(a) ON t TO pk_actor", 0))
				case "all":
					run(referenceExec("GRANT SELECT(b) ON t TO pk_actor", 0))
				case "table":
					run(referenceExec("GRANT SELECT ON t TO pk_actor", 0))
				case "revoked":
					run(referenceExec("REVOKE SELECT ON t FROM pk_actor", 0))
					run(referenceExec("REVOKE SELECT(a,b) ON t FROM pk_actor", 0))
				}
				run(referenceExec("SET SESSION AUTHORIZATION pk_actor", 0))
				want := uniqueReferenceDuplicate("t", "t_pkey", "")
				if mode == "all" || mode == "table" {
					want.Detail = "Key (a, b)=(1, secret) already exists."
				}
				referenceRunStep(t, p.ctx, ex, b.recorder, referenceErrorDiagnostic(query, want, int64(1), "secret"))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			}
		}
	})
}

func TestReferencePrimaryKeyTypeBuildDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "all", "table"} {
		t.Run(mode, func(t *testing.T) {
			roleReferenceCleanup(t, []string{"pk_type_owner"})
			primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				defer a.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				run(referenceExec("CREATE ROLE pk_type_owner", 0))
				if b.pg {
					run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO pk_type_owner", 0))
				}
				run(referenceExec("SET SESSION AUTHORIZATION pk_type_owner", 0))
				run(referenceExec("CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
				run(referenceExec("INSERT INTO t VALUES(1,'protected'),(2,'protected')", 2))
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				before := primaryKeyReferenceIDs(t, p, b, "t_pkey")
				// Owner positive SQL and enforcement are shared. Native owner ACL
				// revocation is exercised through public SetObjectACL in package units;
				// SQL GRANT/REVOKE is intentionally only PostgreSQL here.
				if b.pg {
					run(referenceExec("REVOKE SELECT ON t FROM pk_type_owner", 0))
					if mode == "partial" || mode == "all" {
						run(referenceExec("GRANT SELECT(a) ON t TO pk_type_owner", 0))
					}
					if mode == "all" {
						run(referenceExec("GRANT SELECT(b) ON t TO pk_type_owner", 0))
					}
					if mode == "table" {
						run(referenceExec("GRANT SELECT ON t TO pk_type_owner", 0))
					}
				}
				run(referenceExec("SET SESSION AUTHORIZATION pk_type_owner", 0))
				detail := "Key (a, b)=(0, protected) is duplicated."
				if b.pg && (mode == "none" || mode == "partial") {
					detail = "Duplicate keys exist."
				}
				want := uniqueReferenceDuplicate("t", "t_pkey", detail)
				want.Message = `could not create unique index "t_pkey"`
				s := referencePrepared(referenceErrorDiagnostic("ALTER TABLE t ALTER a TYPE BIGINT USING 0", want))
				s.errorStage = "execute"
				run(s)
				run(referenceExec("RESET SESSION AUTHORIZATION", 0))
				if retained := primaryKeyReferenceIDs(t, p, b, "t_pkey"); retained != before {
					t.Fatal("failed owner TYPE changed identities", before, retained)
				}
				run(referenceSelect("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "protected"}, {int64(2), "protected"}}))
			})
		})
	}
}
