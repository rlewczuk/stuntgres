package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceListPartitionAuthorization(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE USER lp_actor", 0))
		run(fixture.Exec("CREATE TABLE lp_acl(k INT,payload TEXT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_acl_leaf PARTITION OF lp_acl FOR VALUES IN(1,2)", 0))
		run(fixture.Exec("CREATE TABLE lp_acl_expr(k INT) PARTITION BY LIST((k+1))", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO lp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		fixture.WithPublicSchemaOwner(t, p, b, "lp_actor", func() {
			run(referenceRangeError("CREATE TABLE lp_denied PARTITION OF lp_acl FOR VALUES IN(5)", "42501", "must be owner of table lp_acl", fixture.Diagnostic{}))
		})
		for _, table := range []string{"lp_acl", "lp_acl_leaf"} {
			s := referenceRangeError("SELECT * FROM "+table, "42501", "permission denied for table "+table, fixture.Diagnostic{})
			s.SelectRows = true
			run(s)
			run(referenceRangeError("INSERT INTO "+table+" VALUES(1,'denied')", "42501", "permission denied for table "+table, fixture.Diagnostic{}))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		referenceListAbsent(run, "lp_denied")
		run(fixture.Exec("GRANT SELECT,INSERT ON lp_acl TO lp_actor", 0))
		run(fixture.Exec("GRANT INSERT ON lp_acl_expr TO lp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(fixture.Exec("INSERT INTO lp_acl VALUES(1,'parent')", 1))
		run(fixture.Select("SELECT k,payload FROM lp_acl", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}}))
		s := referenceRangeError("SELECT * FROM lp_acl_leaf", "42501", "permission denied for table lp_acl_leaf", fixture.Diagnostic{})
		s.SelectRows = true
		run(s)
		run(referenceRangeError("INSERT INTO lp_acl_leaf VALUES(2,'leaf')", "42501", "permission denied for table lp_acl_leaf", fixture.Diagnostic{}))
		run(referenceRangeMiss("INSERT INTO lp_acl VALUES(7,'visible')", "lp_acl", "k", "7"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON lp_acl FROM lp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(referenceRangeError("INSERT INTO lp_acl VALUES(7,'private')", "23514", `no partition of relation "lp_acl" found for row`, fixture.Diagnostic{Schema: "public", Table: "lp_acl"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON lp_acl TO lp_actor", 0))
		run(fixture.Exec("GRANT SELECT(k) ON lp_acl_expr TO lp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(referenceRangeMiss("INSERT INTO lp_acl VALUES(7,'column')", "lp_acl", "k", "7"))
		run(referenceRangeError("INSERT INTO lp_acl_expr VALUES(5)", "23514", `no partition of relation "lp_acl_expr" found for row`, fixture.Diagnostic{Schema: "public", Table: "lp_acl_expr"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON lp_acl_expr TO lp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(referenceRangeMiss("INSERT INTO lp_acl_expr VALUES(5)", "lp_acl_expr", "(k + 1)", "6"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON lp_acl TO lp_actor", 0))
		run(fixture.Exec("ALTER TABLE lp_acl ENABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(referenceRangeError("INSERT INTO lp_acl VALUES(7,'rls_private')", "23514", `no partition of relation "lp_acl" found for row`, fixture.Diagnostic{Schema: "public", Table: "lp_acl"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE lp_acl DISABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lp_actor", 0))
		run(referenceRangeMiss("INSERT INTO lp_acl VALUES(7,'visible_again')", "lp_acl", "k", "7"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	}, func(t *testing.T) { fixture.RoleReferenceCleanup(t, []string{"lp_actor"}) })
}
