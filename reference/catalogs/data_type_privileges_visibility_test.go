package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDataTypePrivilegesVisibility(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA dtp_private AUTHORIZATION dtp_owner", 0))
		dtpActor(run, "dtp_owner", func() {
			run(fixture.Exec("CREATE TABLE dtp_private.dtp_visible(a integer,b integer,c integer)", 0))
			run(fixture.Exec("REVOKE ALL ON dtp_private.dtp_visible FROM dtp_owner", 0))
			// Ownership independently qualifies even with an empty ordinary ACL.
			run(dtpProjection(t, p, c, "object_schema='dtp_private' AND object_name='dtp_visible'", dtpRows("dtp_private", "dtp_visible", "TABLE", "1", "2", "3")))
		})
		check := func(actor string, ids ...string) {
			dtpActor(run, actor, func() {
				run(dtpProjection(t, p, c, "object_schema='dtp_private' AND object_name='dtp_visible'", dtpRows("dtp_private", "dtp_visible", "TABLE", ids...)))
			})
		}
		check("dtp_reader")
		// No schema USAGE is granted: metadata has no namespace-access filter.
		for _, bit := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			run(fixture.Exec("GRANT "+bit+"(b) ON dtp_private.dtp_visible TO dtp_reader", 0))
			check("dtp_reader", "2")
			run(fixture.Exec("REVOKE "+bit+"(b) ON dtp_private.dtp_visible FROM dtp_reader", 0))
			check("dtp_reader")
		}
		for _, bit := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			run(fixture.Exec("GRANT "+bit+" ON dtp_private.dtp_visible TO dtp_reader", 0))
			check("dtp_reader")
			run(fixture.Exec("REVOKE "+bit+" ON dtp_private.dtp_visible FROM dtp_reader", 0))
		}
		run(fixture.Exec("GRANT INSERT ON dtp_private.dtp_visible TO dtp_reader", 0))
		check("dtp_reader", "1", "2", "3")
		run(fixture.Exec("GRANT dtp_reader TO dtp_member WITH INHERIT TRUE,SET TRUE", 0))
		run(fixture.Exec("GRANT dtp_reader TO dtp_set WITH INHERIT FALSE,SET TRUE", 0))
		check("dtp_member", "1", "2", "3")
		check("dtp_set")
		dtpActor(run, "dtp_set", func() {
			run(fixture.Exec("SET ROLE dtp_reader", 0))
			run(dtpProjection(t, p, c, "object_schema='dtp_private' AND object_name='dtp_visible'", dtpRows("dtp_private", "dtp_visible", "TABLE", "1", "2", "3")))
			run(fixture.Exec("RESET ROLE", 0))
		})
		run(fixture.Exec("REVOKE INSERT ON dtp_private.dtp_visible FROM dtp_reader", 0))
		run(fixture.Exec("GRANT SELECT(a) ON dtp_private.dtp_visible TO PUBLIC", 0))
		check("dtp_reader", "1")
		run(fixture.Exec("REVOKE SELECT(a) ON dtp_private.dtp_visible FROM PUBLIC", 0))
		run(fixture.Exec("GRANT dtp_owner TO dtp_member WITH INHERIT TRUE", 0))
		check("dtp_member", "1", "2", "3")
	})
}

func TestReferenceDataTypePrivilegesTypeVisibility(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA dtp_types AUTHORIZATION dtp_owner", 0))
		dtpActor(run, "dtp_owner", func() {
			run(fixture.Exec("CREATE DOMAIN dtp_types.dtp_public_domain AS integer", 0))
			run(fixture.Exec("CREATE TYPE dtp_types.dtp_public_composite AS (a integer)", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
			run(fixture.Exec("CREATE DOMAIN dtp_types.dtp_hidden_domain AS varchar(7)", 0))
			run(fixture.Exec("CREATE TYPE dtp_types.dtp_hidden_composite AS (a integer,b integer[])", 0))
			run(fixture.Exec("CREATE TABLE dtp_types.dtp_hidden_usage(a dtp_types.dtp_hidden_domain,b dtp_types.dtp_hidden_composite,c dtp_types.dtp_hidden_domain[])", 0))
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON TYPES TO dtp_reader", 0))
			run(fixture.Exec("CREATE DOMAIN dtp_types.dtp_reader_domain AS integer[]", 0))
			run(fixture.Exec("CREATE TYPE dtp_types.dtp_reader_composite AS (a varchar(7)[],b integer)", 0))
		})
		run(fixture.Exec("GRANT SELECT ON dtp_types.dtp_hidden_usage TO dtp_reader", 0))
		publicRows := dtpRows("dtp_types", "dtp_public_domain", "DOMAIN", "1")
		publicRows = append(publicRows, dtpRows("dtp_types", "dtp_public_composite", "USER-DEFINED TYPE", "1")...)
		readerRows := append([][]any{}, publicRows...)
		readerRows = append(readerRows, dtpRows("dtp_types", "dtp_reader_domain", "DOMAIN", "1")...)
		readerRows = append(readerRows, dtpRows("dtp_types", "dtp_reader_composite", "USER-DEFINED TYPE", "1", "2")...)
		readerRows = append(readerRows, dtpRows("dtp_types", "dtp_hidden_usage", "TABLE", "1", "2", "3")...)
		dtpActor(run, "dtp_reader", func() { run(dtpProjection(t, p, c, "object_schema='dtp_types'", readerRows)) })
		dtpActor(run, "dtp_set", func() { run(dtpProjection(t, p, c, "object_schema='dtp_types'", publicRows)) })
		ownerRows := append([][]any{}, readerRows...)
		ownerRows = append(ownerRows, dtpRows("dtp_types", "dtp_hidden_domain", "DOMAIN", "1")...)
		ownerRows = append(ownerRows, dtpRows("dtp_types", "dtp_hidden_composite", "USER-DEFINED TYPE", "1", "2")...)
		dtpActor(run, "dtp_owner", func() { run(dtpProjection(t, p, c, "object_schema='dtp_types'", ownerRows)) })
	})
}

func TestReferenceDataTypePrivilegesKindsAndTemp(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE TABLE dtp_kind_table(a integer,b text)",
			"CREATE VIEW dtp_kind_view AS SELECT a FROM dtp_kind_table",
			"CREATE MATERIALIZED VIEW dtp_kind_mat AS SELECT a FROM dtp_kind_table",
			"CREATE INDEX dtp_kind_index ON dtp_kind_table(a)",
			"CREATE SEQUENCE dtp_kind_sequence",
			"CREATE TABLE dtp_kind_parent(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE dtp_kind_child PARTITION OF dtp_kind_parent FOR VALUES FROM(0) TO(10)",
		} {
			run(fixture.Exec(q, 0))
		}
		want := dtpRows("public", "dtp_kind_table", "TABLE", "1", "2")
		for _, name := range []string{"dtp_kind_view", "dtp_kind_parent", "dtp_kind_child"} {
			want = append(want, dtpRows("public", name, "TABLE", "1")...)
		}
		run(dtpProjection(t, p, c, "object_schema='public' AND object_name IN ('dtp_kind_table','dtp_kind_view','dtp_kind_mat','dtp_kind_index','dtp_kind_sequence','dtp_kind_parent','dtp_kind_child')", want))
		run(fixture.Exec("CREATE TEMP TABLE dtp_own_temp(a integer,b integer[])", 0))
		var namespace string
		if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("CREATE TEMP TABLE dtp_peer_temp(a integer)", 0))
		run(dtpProjection(t, p, c, "object_name IN ('dtp_own_temp','dtp_peer_temp')", dtpRows(namespace, "dtp_own_temp", "TABLE", "1", "2")))
		// Same physical session remains the owner of its temporary namespace.
		run(fixture.Exec("GRANT SELECT ON dtp_own_temp TO dtp_reader", 0))
		run(fixture.Exec("SET ROLE dtp_reader", 0))
		run(dtpProjection(t, p, c, "object_name IN ('dtp_own_temp','dtp_peer_temp')", dtpRows(namespace, "dtp_own_temp", "TABLE", "1", "2")))
		run(fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceDataTypePrivilegesDiagnostics(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		s := fixture.ImplicitError("SELECT missing FROM information_schema.data_type_privileges", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
		// Resolution happens at Prepare for the prepared route, at execution otherwise.
		// RunStep accepts the default phase when no explicit ErrorStage is supplied.
		run(s)
		run(fixture.Exec("CREATE TABLE dtp_diagnostic_sentinel(a integer)", 0))
		run(fixture.Exec("INSERT INTO dtp_diagnostic_sentinel VALUES(7)", 1))
		for _, mutation := range []struct{ query, verb, action, upper string }{
			{"INSERT INTO information_schema.data_type_privileges(object_name) VALUES('dtp_invented')", "insert into", "inserting into", "INSERT"},
			{"UPDATE information_schema.data_type_privileges SET object_name='dtp_invented' WHERE FALSE", "update", "updating", "UPDATE"},
			{"DELETE FROM information_schema.data_type_privileges WHERE FALSE", "delete from", "deleting from", "DELETE"},
		} {
			run(fixture.ImplicitError(mutation.query, fixture.Diagnostic{Code: "55000", Message: "cannot " + mutation.verb + ` view "data_type_privileges"`, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + mutation.action + " the view, provide an INSTEAD OF " + mutation.upper + " trigger or an unconditional ON " + mutation.upper + " DO INSTEAD rule."}))
			run(fixture.Select("SELECT a FROM dtp_diagnostic_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(dtpProjection(t, p, c, "object_name='dtp_invented'", nil))
		}
		lockQuery := "SELECT * FROM information_schema.data_type_privileges WHERE FALSE FOR UPDATE"
		if b.PG {
			// The pinned oracle permits this empty locking query on the view.
			empty := fixture.Select(lockQuery, dtpColumns, dtpTypes, nil)
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil { t.Fatal(err) }
				fixture.TableDDLColumnTypes(t, true, types, dtpColumns, dtpTypes, []bool{true,true,true,true,true})
			}
			run(empty)
		} else {
			run(fixture.ImplicitError(lockQuery, fixture.Diagnostic{Code:"0A000", Message:"row locking is not supported for virtual catalog relations"}))
		}
		run(dtpProjection(t, p, c, "object_schema='public' AND object_name='dtp_diagnostic_absent'", nil))
	})
}
