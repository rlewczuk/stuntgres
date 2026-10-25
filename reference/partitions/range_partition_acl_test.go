package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionACL(t *testing.T) {
	for _, route := range []struct {
		name     string
		prepared bool
	}{{"direct", false}, {"prepared", true}} {
		t.Run(route.name, func(t *testing.T) {
			referenceRangePartitionACLCase(t, route.prepared)
		})
	}
}

func referenceRangePartitionACLCase(t *testing.T, prepared bool) {
	fixture.RoleReferenceCleanup(t, []string{"rp_actor"})
	fixture.SchemaReferenceCleanup(t, "rp_private")
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
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
		run := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		run(fixture.Exec("CREATE USER rp_actor", 0))
		run(fixture.Exec("CREATE TABLE rp_acl(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE rp_acl_leaf PARTITION OF rp_acl FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE rp_acl_expr(k INT) PARTITION BY RANGE((k+1))", 0))
		run(fixture.Exec("CREATE SCHEMA rp_private", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rp_actor", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_actor", 0))
		s := referenceRangeError("CREATE TABLE rp_denied PARTITION OF rp_acl FOR VALUES FROM(3) TO(5)", "42501", "must be owner of table rp_acl", fixture.Diagnostic{})
		s.Prepared = prepared
		fixture.WithPublicSchemaOwner(t, p, b, "rp_actor", func() { run(s) })
		for _, table := range []string{"rp_acl", "rp_acl_leaf"} {
			s = referenceRangeError("SELECT * FROM "+table, "42501", "permission denied for table "+table, fixture.Diagnostic{})
			s.Prepared = prepared
			s.SelectRows = true
			s.ErrorStage = "execute"
			if b.PG && prepared {
				s.ErrorStage = "execute"
			}
			run(s)
			s = referenceRangeError("INSERT INTO "+table+" VALUES(1,'denied')", "42501", "permission denied for table "+table, fixture.Diagnostic{})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_denied'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT * FROM rp_acl", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
		// Public table/column grants exercise the same named-target authority
		// and missing-key disclosure policy in both backends.
		run(fixture.Exec("GRANT SELECT,INSERT ON rp_acl TO rp_actor", 0))
		run(fixture.Exec("GRANT INSERT ON rp_acl_expr TO rp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_actor", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_acl VALUES(1,'parent')", 1)))
		run(fixture.Prepared(fixture.Select("SELECT * FROM rp_acl", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "parent"}})))
		s = referenceRangeError("SELECT * FROM rp_acl_leaf", "42501", "permission denied for table rp_acl_leaf", fixture.Diagnostic{})
		s.SelectRows = true
		run(s)
		run(referenceRangeError("INSERT INTO rp_acl_leaf VALUES(2,'leaf')", "42501", "permission denied for table rp_acl_leaf", fixture.Diagnostic{}))
		run(referenceRangeMiss("INSERT INTO rp_acl VALUES(5,'visible')", "rp_acl", "k", "5"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON rp_acl FROM rp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_actor", 0))
		run(referenceRangeError("INSERT INTO rp_acl VALUES(5,'private')", "23514", `no partition of relation "rp_acl" found for row`, fixture.Diagnostic{Schema: "public", Table: "rp_acl"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON rp_acl TO rp_actor", 0))
		run(fixture.Exec("GRANT SELECT(k) ON rp_acl_expr TO rp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_actor", 0))
		run(referenceRangeMiss("INSERT INTO rp_acl VALUES(5,'column')", "rp_acl", "k", "5"))
		run(referenceRangeError("INSERT INTO rp_acl_expr VALUES(5)", "23514", `no partition of relation "rp_acl_expr" found for row`, fixture.Diagnostic{Schema: "public", Table: "rp_acl_expr"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON rp_acl_expr TO rp_actor", 0))
		run(fixture.Exec("REVOKE ALL ON rp_acl FROM rp_actor", 0))
		run(fixture.Exec("REVOKE SELECT(k) ON rp_acl FROM rp_actor", 0))
		run(fixture.Exec("GRANT SELECT,INSERT ON rp_acl_leaf TO rp_actor", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_actor", 0))
		run(referenceRangeMiss("INSERT INTO rp_acl_expr VALUES(5)", "rp_acl_expr", "(k + 1)", "6"))
		run(fixture.Exec("INSERT INTO rp_acl_leaf VALUES(2,'leaf')", 1))
		run(fixture.Select("SELECT k FROM rp_acl_leaf ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		s = referenceRangeError("SELECT * FROM rp_acl", "42501", "permission denied for table rp_acl", fixture.Diagnostic{})
		s.SelectRows = true
		run(s)
		run(referenceRangeError("CREATE TABLE rp_private.denied PARTITION OF rp_acl FOR VALUES FROM(3) TO(5)", "42501", "permission denied for schema rp_private", fixture.Diagnostic{Position: "14"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

// Valid simple keys retain raw names in the catalog, but their authorized
// missing-range detail uses PostgreSQL identifier spelling. INSERT-only
// authority must still suppress that detail, including at a nested failure.
func TestReferenceRangePartitionQuotedKeyPrivacy(t *testing.T) {
	for _, key := range []struct {
		name string
		sql  string
	}{
		{"mixed_space", `"Odd Name"`},
		{"embedded_quote", `"a""b"`},
		{"reserved", `"select"`},
	} {
		t.Run(key.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				t.Run(route, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"rp_quoted_actor"})
					referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
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
						run := func(s fixture.Step) {
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
						run(fixture.Exec("CREATE USER rp_quoted_actor", 0))
						run(fixture.Exec("CREATE TABLE rp_quoted_root("+key.sql+" INT,payload TEXT) PARTITION BY RANGE("+key.sql+")", 0))
						run(fixture.Exec("CREATE TABLE rp_quoted_middle PARTITION OF rp_quoted_root FOR VALUES FROM(0) TO(10) PARTITION BY RANGE("+key.sql+")", 0))
						run(fixture.Exec("CREATE TABLE rp_quoted_leaf PARTITION OF rp_quoted_middle FOR VALUES FROM(0) TO(3)", 0))
						run(fixture.Exec("INSERT INTO rp_quoted_root VALUES(1,'kept')", 1))
						if b.PG {
							run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rp_quoted_actor", 0))
						}
						run(fixture.Exec("GRANT INSERT ON rp_quoted_root,rp_quoted_middle TO rp_quoted_actor", 0))
						failures := []struct {
							query string
							table string
							value string
						}{
							{"INSERT INTO rp_quoted_root VALUES(15,'hidden')", "rp_quoted_root", "15"},
							{"INSERT INTO rp_quoted_root VALUES(5,'hidden')", "rp_quoted_middle", "5"},
							{"INSERT INTO rp_quoted_middle VALUES(5,'hidden')", "rp_quoted_middle", "5"},
						}
						hidden := func() {
							for _, failure := range failures {
								run(referenceRangeError(failure.query, "23514", `no partition of relation "`+failure.table+`" found for row`, fixture.Diagnostic{Schema: "public", Table: failure.table}))
							}
						}
						run(fixture.Exec("SET SESSION AUTHORIZATION rp_quoted_actor", 0))
						hidden()
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Exec("GRANT SELECT("+key.sql+") ON rp_quoted_root,rp_quoted_middle TO rp_quoted_actor", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION rp_quoted_actor", 0))
						for _, failure := range failures {
							run(referenceRangeMiss(failure.query, failure.table, key.sql, failure.value))
						}
						for _, target := range []string{"rp_quoted_root", "rp_quoted_middle"} {
							visible := fixture.Select("SELECT "+key.sql+" AS k FROM "+target, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})
							visible.CheckMetadata = referenceRangeMetadata(b.PG, []string{"k"}, []string{"INT4"}, []bool{true})
							run(visible)
							denied := referenceRangeError("SELECT payload FROM "+target, "42501", "permission denied for table "+target, fixture.Diagnostic{})
							denied.SelectRows = true
							run(denied)
						}
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						run(fixture.Exec("REVOKE SELECT("+key.sql+") ON rp_quoted_root,rp_quoted_middle FROM rp_quoted_actor", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION rp_quoted_actor", 0))
						hidden()
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						// Every failed batch leaves the admitted physical prefix
						// unchanged, regardless of named root or nested target.
						for _, target := range []string{"rp_quoted_root", "rp_quoted_middle", "rp_quoted_leaf"} {
							s := fixture.Select("SELECT "+key.sql+" AS k,payload FROM "+target, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}})
							s.CheckMetadata = referenceRangeMetadata(b.PG, []string{"k", "payload"}, []string{"INT4", "TEXT"}, []bool{true, true})
							run(s)
						}
					})
				})
			}
		})
	}
}
