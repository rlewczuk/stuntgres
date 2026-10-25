package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachPrivacy(t *testing.T) {
	for _, sparse := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := "reordered/direct"
			if sparse {
				name = "sparse/direct"
			}
			if prepared {
				name = name[:len(name)-len("direct")] + "prepared"
			}
			t.Run(name, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"pa_privacy_actor"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
						run(fixture.Exec("CREATE ROLE pa_privacy_actor", 0))
						run(fixture.Exec("CREATE TABLE pa_privacy_parent(k INT,payload TEXT,secret TEXT DEFAULT 'parent_secret') PARTITION BY RANGE(k)", 0))
						extra := ""
						if sparse {
							extra = "obsolete INT,"
						}
						run(fixture.Exec("CREATE TABLE pa_privacy_child("+extra+"secret TEXT DEFAULT 'child_secret',payload TEXT NOT NULL,k INT,CONSTRAINT pa_privacy_check CHECK(payload<>'bad'),CONSTRAINT pa_privacy_key UNIQUE(k,secret))", 0))
						if sparse {
							run(fixture.Exec("ALTER TABLE pa_privacy_child DROP COLUMN obsolete", 0))
						}
						run(fixture.Exec("INSERT INTO pa_privacy_child(secret,payload,k) VALUES('seed_secret','seed',1)", 1))
						attach := fixture.Exec("ALTER TABLE pa_privacy_parent ATTACH PARTITION pa_privacy_child FOR VALUES FROM(1) TO(5)", 0)
						attach.Prepared = prepared
						run(attach)
						if b.PG {
							run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pa_privacy_actor", 0))
						} else {
							// Native schema GRANT is excluded; only namespace ownership
							// changes. Both tables remain bootstrap-owned.
							run(fixture.Exec("ALTER SCHEMA public OWNER TO pa_privacy_actor", 0))
						}
						run(fixture.Exec("GRANT INSERT ON pa_privacy_parent,pa_privacy_child TO pa_privacy_actor", 0))
						unchanged := func() {
							run(fixture.Select("SELECT * FROM pa_privacy_parent", []string{"k", "payload", "secret"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "seed", "seed_secret"}}))
							run(fixture.Select("SELECT * FROM pa_privacy_child", []string{"secret", "payload", "k"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"seed_secret", "seed", int64(1)}}))
						}
						// Prepare under the restricted identity once and reuse the actual
						// held statement across grants, revocation and RLS transitions.
						statements := make(map[string]*sql.Stmt)
						defer func() {
							for _, stmt := range statements {
								if err := stmt.Close(); err != nil {
									t.Error(err)
								}
							}
						}()
						fail := func(q string, want fixture.Diagnostic) {
							run(fixture.Exec("SET SESSION AUTHORIZATION pa_privacy_actor", 0))
							s := fixture.ImplicitError(q, want)
							s.ErrorStage = "execute"
							if prepared {
								stmt := statements[q]
								if stmt == nil {
									stmt, err = conn.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									statements[q] = stmt
								}
								fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
							} else {
								run(s)
							}
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							unchanged()
						}
						for _, target := range []string{"parent", "child"} {
							table := "pa_privacy_" + target
							for _, acl := range []string{"supplied", "read_default", "revoked", "table_select"} {
								switch acl {
								case "read_default":
									run(fixture.Exec("GRANT SELECT(secret) ON "+table+" TO pa_privacy_actor", 0))
								case "revoked":
									run(fixture.Exec("REVOKE SELECT(secret) ON "+table+" FROM pa_privacy_actor", 0))
								case "table_select":
									run(fixture.Exec("GRANT SELECT ON "+table+" TO pa_privacy_actor", 0))
								}
								for _, check := range []bool{false, true} {
									value, rendered := "NULL::text", "null"
									want := fixture.Diagnostic{Code: "23502", Message: `null value in column "payload" of relation "pa_privacy_child" violates not-null constraint`, Schema: "public", Table: "pa_privacy_child", Column: "payload"}
									if check {
										value, rendered = "'bad'::text", "bad"
										want = fixture.Diagnostic{Code: "23514", Message: `new row for relation "pa_privacy_child" violates check constraint "pa_privacy_check"`, Schema: "public", Table: "pa_privacy_child", Constraint: "pa_privacy_check"}
									}
									if target == "parent" {
										want.Detail = "Failing row contains (k, payload) = (2, " + rendered + ")."
										if acl == "read_default" {
											want.Detail = "Failing row contains (k, payload, secret) = (2, " + rendered + ", parent_secret)."
										} else if acl == "table_select" {
											want.Detail = "Failing row contains (2, " + rendered + ", parent_secret)."
										}
									} else {
										want.Detail = "Failing row contains (payload, k) = (" + rendered + ", 2)."
										if acl == "read_default" {
											want.Detail = "Failing row contains (secret, payload, k) = (child_secret, " + rendered + ", 2)."
										} else if acl == "table_select" {
											want.Detail = "Failing row contains (child_secret, " + rendered + ", 2)."
										}
									}
									for _, source := range []string{" VALUES(2," + value + ")", " SELECT 2," + value} {
										fail("INSERT INTO "+table+"(k,payload)"+source, want)
									}
								}
							}
							run(fixture.Exec("REVOKE SELECT ON "+table+" FROM pa_privacy_actor", 0))
						}
						run(fixture.Exec("GRANT SELECT ON pa_privacy_parent TO pa_privacy_actor", 0))
						for _, acl := range []string{"parent_only", "one_key", "all_keys", "revoked", "table_select", "leaf_rls"} {
							switch acl {
							case "one_key":
								run(fixture.Exec("GRANT SELECT(k) ON pa_privacy_child TO pa_privacy_actor", 0))
							case "all_keys":
								run(fixture.Exec("GRANT SELECT(secret) ON pa_privacy_child TO pa_privacy_actor", 0))
							case "revoked":
								run(fixture.Exec("REVOKE SELECT(k,secret) ON pa_privacy_child FROM pa_privacy_actor", 0))
							case "table_select":
								run(fixture.Exec("GRANT SELECT ON pa_privacy_child TO pa_privacy_actor", 0))
							case "leaf_rls":
								run(fixture.Exec("ALTER TABLE pa_privacy_child ENABLE ROW LEVEL SECURITY", 0))
							}
							want := fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "pa_privacy_key"`, Schema: "public", Table: "pa_privacy_child", Constraint: "pa_privacy_key"}
							if acl == "all_keys" || acl == "table_select" {
								want.Detail = "Key (k, secret)=(1, seed_secret) already exists."
							}
							for _, source := range []string{" VALUES(1,'new','seed_secret')", " SELECT 1,'new','seed_secret'"} {
								fail("INSERT INTO pa_privacy_parent(k,payload,secret)"+source, want)
								if acl != "leaf_rls" {
									fail("INSERT INTO pa_privacy_child(k,payload,secret)"+source, want)
								}
							}
						}
						// No policies means actual default deny, not a bypass to get
						// constraint diagnostics. Leaf policy checks do not govern a
						// parent-targeted insert, but direct leaf proposals are denied.
						for _, source := range []string{" VALUES(2,NULL::text)", " SELECT 2,NULL::text"} {
							fail("INSERT INTO pa_privacy_child(k,payload)"+source, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "pa_privacy_child"`})
						}
						// An applicable granting policy exposes constraint diagnostics.
						run(fixture.Exec("CREATE POLICY pa_privacy_insert ON pa_privacy_child FOR INSERT TO pa_privacy_actor WITH CHECK(TRUE)", 0))
						want := fixture.Diagnostic{Code: "23502", Message: `null value in column "payload" of relation "pa_privacy_child" violates not-null constraint`, Schema: "public", Table: "pa_privacy_child", Column: "payload"}
						for _, source := range []string{" VALUES(2,NULL::text)", " SELECT 2,NULL::text"} {
							fail("INSERT INTO pa_privacy_child(k,payload)"+source, want)
						}
						run(fixture.Exec("ALTER TABLE pa_privacy_child DISABLE ROW LEVEL SECURITY", 0))
						run(fixture.Exec("ALTER TABLE pa_privacy_parent ENABLE ROW LEVEL SECURITY", 0))
						for _, source := range []string{" VALUES(2,NULL::text)", " SELECT 2,NULL::text"} {
							fail("INSERT INTO pa_privacy_parent(k,payload)"+source, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "pa_privacy_parent"`})
						}
						for _, source := range []string{" VALUES(9,'outside')", " SELECT 9,'outside'"} {
							fail("INSERT INTO pa_privacy_parent(k,payload)"+source, fixture.Diagnostic{Code: "23514", Message: `no partition of relation "pa_privacy_parent" found for row`, Schema: "public", Table: "pa_privacy_parent"})
						}
						unchanged()
					})
				}
			})
		}
	}
}
