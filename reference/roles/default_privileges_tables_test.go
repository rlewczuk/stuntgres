package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleDefaultPrivilegesTables(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpTable(run, "dp_old", 7)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		dpTable(run, "dp_new", 9)
		dpActor(run, "dp_reader", func() { dpDenied(run, "SELECT x FROM dp_old", "table", "dp_old"); dpRead(run, "dp_new", 9) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_next", 11)
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_new", 9); dpDenied(run, "SELECT x FROM dp_next", "table", "dp_next") })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		run(fixture.Exec("CREATE VIEW dp_view AS SELECT x FROM dp_old", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW dp_mv AS SELECT x FROM dp_old", 1))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_view", 7); dpRead(run, "dp_mv", 7) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("CREATE OR REPLACE VIEW dp_view AS SELECT x FROM dp_next", 0))
		run(fixture.Exec("CREATE VIEW dp_view_next AS SELECT x FROM dp_old", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW dp_mv_next AS SELECT x FROM dp_old", 1))
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_view", 11)
			dpDenied(run, "SELECT x FROM dp_view_next", "view", "dp_view_next")
			dpDenied(run, "SELECT x FROM dp_mv_next", "materialized view", "dp_mv_next")
		})
		run(fixture.Exec("CREATE TABLE IF NOT EXISTS dp_new(x INT)", 0))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_new", 9) })
	})
}

func TestReferenceRoleDefaultPrivilegesCreation(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		owner, _ := dpEnvironment(t, p, c)
		run(fixture.Exec("CREATE TYPE dp_typed_source AS(x INT)", 0))
		for _, q := range []string{"CREATE TABLE dp_typed_old OF dp_typed_source", "CREATE TEMP TABLE dp_typed_temp_old OF dp_typed_source"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO dp_typed_old VALUES(7)", 1))
		run(fixture.Exec("INSERT INTO dp_typed_temp_old VALUES(7)", 1))
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "SELECT x FROM dp_typed_old", "table", "dp_typed_old")
			dpDenied(run, "SELECT x FROM dp_typed_temp_old", "table", "dp_typed_temp_old")
		})
		dpTable(run, "dp_source", 7)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader", 0))
		for _, tc := range []struct{ q, name string }{
			{"CREATE TABLE dp_like(LIKE dp_source)", "dp_like"},
			{"CREATE TABLE dp_inherit() INHERITS(dp_source)", "dp_inherit"},
			{"CREATE TEMP TABLE dp_temp(x INT)", "dp_temp"},
			{"CREATE TABLE dp_typed_new OF dp_typed_source", "dp_typed_new"},
			{"CREATE TEMP TABLE dp_typed_temp_new OF dp_typed_source", "dp_typed_temp_new"},
		} {
			run(fixture.Exec(tc.q, 0))
			run(fixture.Exec("INSERT INTO "+tc.name+" VALUES(9)", 1))
			dpActor(run, "dp_reader", func() { dpRead(run, tc.name, 9) })
		}
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_typed_new' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_typed_new", "SELECT", "NO", "YES"}})
		dpActor(run, "dp_reader", func() {
			dpDenied(run, "SELECT x FROM dp_typed_old", "table", "dp_typed_old")
			dpDenied(run, "SELECT x FROM dp_typed_temp_old", "table", "dp_typed_temp_old")
		})
		// Physical TEMP namespace authority survives actor switches, but never
		// substitutes for the materialized table ACL or exposes a peer's path.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		peerRun := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
		}
		peerRun(fixture.Exec("SET SESSION AUTHORIZATION dp_reader", 0))
		missing := fixture.ImplicitError("SELECT x FROM dp_temp", fixture.Diagnostic{Code: "42P01", Message: `relation "dp_temp" does not exist`, Position: "15"})
		missing.SelectRows = true
		missing.ErrorStage = "execute"
		if prepared {
			missing.ErrorStage = "prepare"
		}
		peerRun(missing)
		peerRun(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON dp_temp FROM dp_reader", 0))
		dpActor(run, "dp_reader", func() { dpDenied(run, "SELECT x FROM dp_temp", "table", "dp_temp") })
		// The integrated recursive scan retains each descendant's default ACL.
		dpRead(run, "dp_source", 7, 9)
		// Supported query creation has its own independently populated ordinary
		// source, so both routes really create two rows without recursive scans.
		run(fixture.Exec("CREATE TABLE dp_query_source(x INT)", 0))
		run(fixture.Exec("INSERT INTO dp_query_source VALUES(7),(9)", 2))
		run(fixture.Exec("CREATE TABLE dp_ctas AS SELECT x FROM dp_query_source", 2))
		run(fixture.Exec("SELECT x INTO dp_into FROM dp_query_source", 2))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_ctas", 7, 9); dpRead(run, "dp_into", 7, 9) })
		run(fixture.Exec("CREATE TABLE dp_partition(x INT) PARTITION BY RANGE(x)", 0))
		run(fixture.Exec("CREATE TABLE dp_intermediate PARTITION OF dp_partition FOR VALUES FROM(0) TO(20) PARTITION BY RANGE(x)", 0))
		run(fixture.Exec("CREATE TABLE dp_child PARTITION OF dp_intermediate FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("INSERT INTO dp_partition VALUES(9)", 1))
		dpActor(run, "dp_reader", func() { dpRead(run, "dp_partition", 9); dpRead(run, "dp_intermediate", 9); dpRead(run, "dp_child", 9) })
		// Parent/source grants cannot supply a new descriptor's ACL after reversal.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
		run(fixture.Exec("CREATE TABLE dp_like_next(LIKE dp_like)", 0))
		run(fixture.Exec("CREATE TABLE dp_inherit_next() INHERITS(dp_like)", 0))
		run(fixture.Exec("CREATE TABLE dp_child_next PARTITION OF dp_intermediate FOR VALUES FROM(10) TO(20)", 0))
		run(fixture.Exec("CREATE TABLE dp_typed_next OF dp_typed_source", 0))
		run(fixture.Exec("CREATE TEMP TABLE dp_typed_temp_next OF dp_typed_source", 0))
		dpActor(run, "dp_reader", func() {
			for _, name := range []string{"dp_like_next", "dp_inherit_next", "dp_child_next", "dp_typed_next", "dp_typed_temp_next"} {
				dpDenied(run, "SELECT x FROM "+name, "table", name)
			}
		})
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_typed_new", 9)
			dpRead(run, "dp_typed_temp_new", 9)
		})
		dpProjection(t, p, c, run, "table_schema='public' AND table_name='dp_typed_new' AND grantee='dp_reader'", [][]any{{owner, "dp_reader", "public", "dp_typed_new", "SELECT", "NO", "YES"}})
		dpProjection(t, p, c, run, "table_schema='public' AND (table_name='dp_typed_old' OR table_name='dp_typed_next') AND grantee='dp_reader'", nil)
	})
}

func TestReferenceRoleDefaultPrivilegesOwnerOptions(t *testing.T) {
	dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		dpSchema(run, "dp_owner_ns", "dp_owner")
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE SELECT ON TABLES FROM dp_owner", 0))
		dpActor(run, "dp_owner", func() {
			dpTable(run, "dp_owner_ns.dp_no_owner", 7)
			dpDenied(run, "SELECT x FROM dp_owner_ns.dp_no_owner", "table", "dp_no_owner")
			run(fixture.Exec("GRANT SELECT ON dp_owner_ns.dp_no_owner TO dp_owner", 0))
			dpRead(run, "dp_owner_ns.dp_no_owner", 7)
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_owner", 0))
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner GRANT SELECT ON TABLES TO dp_reader WITH GRANT OPTION", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_options_old", 9) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE GRANT OPTION FOR SELECT ON TABLES FROM dp_reader CASCADE", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_options_new", 11) })
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_ns.dp_options_old", 9)
			dpRead(run, "dp_owner_ns.dp_options_new", 11)
			run(fixture.Exec("GRANT SELECT ON dp_owner_ns.dp_options_old TO dp_other", 0))
		})
		// Exercise both successful old delegation and denied future delegation through
		// public tables, keeping namespace rights independent from table grant options.
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader WITH GRANT OPTION", 0))
		dpTable(run, "dp_delegate", 9)
		dpActor(run, "dp_reader", func() { run(fixture.Exec("GRANT SELECT ON dp_delegate TO dp_other", 0)) })
		dpActor(run, "dp_other", func() { dpRead(run, "dp_delegate", 9) })
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE GRANT OPTION FOR SELECT ON TABLES FROM dp_reader", 0))
		dpTable(run, "dp_delegate_next", 11)
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION dp_reader", 0))
		q := "GRANT SELECT ON dp_delegate_next TO dp_other"
		statement, err := nc.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		if len(notices) != 0 {
			t.Fatal("Prepare must be quiet", notices)
		}
		s := fixture.Exec(q, 0)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, nc, recorder, s)
		warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "dp_delegate_next"`}
		if len(notices) != 1 || notices[0] != warning {
			t.Fatalf("future delegation notices=%#v want %#v", notices, warning)
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_delegate_next", 11)
			run(fixture.Exec("GRANT SELECT ON dp_delegate TO dp_member", 0))
		})
		dpActor(run, "dp_member", func() { dpRead(run, "dp_delegate", 9) })
		dpActor(run, "dp_other", func() { dpDenied(run, "SELECT x FROM dp_delegate_next", "table", "dp_delegate_next") })
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_options_new' AND grantee='dp_reader'", [][]any{{"dp_owner", "dp_reader", "dp_owner_ns", "dp_options_new", "SELECT", "NO", "YES"}})
		dpProjection(t, p, c, run, "table_schema='dp_owner_ns' AND table_name='dp_options_old' AND grantee='dp_reader'", [][]any{{"dp_owner", "dp_reader", "dp_owner_ns", "dp_options_old", "SELECT", "YES", "YES"}})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE dp_owner REVOKE SELECT ON TABLES FROM dp_reader", 0))
		dpActor(run, "dp_owner", func() { dpTable(run, "dp_owner_ns.dp_options_next", 13) })
		dpActor(run, "dp_reader", func() {
			dpRead(run, "dp_owner_ns.dp_options_new", 11)
			dpDenied(run, "SELECT x FROM dp_owner_ns.dp_options_next", "table", "dp_options_next")
		})
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE GRANT OPTION FOR SELECT ON TABLES FROM PUBLIC", 0))
		dpError(run, "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles")
	})
}
