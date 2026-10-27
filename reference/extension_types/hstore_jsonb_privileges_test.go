package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONBPrivileges(t *testing.T) {
	for _, name := range []string{"hstore_to_jsonb", "hstore_to_jsonb_loose"} {
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"hs_jsonb_actor"})
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, q := range []string{"CREATE ROLE hs_jsonb_actor", "GRANT USAGE ON SCHEMA public TO hs_jsonb_actor", "CREATE TABLE hs_jsonb_acl(id integer,v hstore)", "GRANT SELECT ON hs_jsonb_acl TO hs_jsonb_actor"} {
					hstoreRun(t, p, b, c, fixture.Exec(q, 0))
				}
				hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_acl VALUES (1,'a=>1'),(2,NULL)", 2))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_actor", 0))
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
				}()
				var held []fixture.HeldStatement
				for _, id := range []string{"1", "2"} {
					q := "SELECT " + name + "(v) AS j FROM hs_jsonb_acl WHERE id=" + id
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
				}
				value := `{"a": "1"}`
				if name == "hstore_to_jsonb_loose" {
					value = `{"a": 1}`
				}
				for i, h := range held {
					var want any = value
					if i == 1 {
						want = nil
					}
					hstoreRun(t, p, b, h, hstoreJSONBSelect(b, h.SQL, []string{"j"}, [][]any{{want}}, false))
				}
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+name+"(hstore) FROM PUBLIC,hs_jsonb_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_actor", 0))
				denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
				for _, h := range held {
					hstoreError(t, p, b, c, h.SQL, false, true, false, denied)
					hstoreError(t, p, b, c, h.SQL, true, true, false, denied)
					hstoreError(t, p, b, h, h.SQL, false, true, false, denied)
				}
				q := "SELECT " + name + "(NULL::hstore) AS j"
				s := hstoreJSONBSelect(b, q, []string{"j"}, [][]any{{nil}}, false)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION "+name+"(hstore) TO hs_jsonb_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_actor", 0))
				for i, h := range held {
					var want any = value
					if i == 1 {
						want = nil
					}
					hstoreRun(t, p, b, h, hstoreJSONBSelect(b, h.SQL, []string{"j"}, [][]any{{want}}, false))
				}
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			})
		})
	}
	t.Run("cast_execute_policy", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"hs_jsonb_cast_actor"})
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			for _, q := range []string{"CREATE ROLE hs_jsonb_cast_actor", "GRANT USAGE ON SCHEMA public TO hs_jsonb_cast_actor", "CREATE TABLE hs_jsonb_cast_acl(id integer,v hstore)", "GRANT SELECT ON hs_jsonb_cast_acl TO hs_jsonb_cast_actor"} {
				hstoreRun(t, p, b, c, fixture.Exec(q, 0))
			}
			hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_cast_acl VALUES (1,'a=>1'),(2,NULL)", 2))
			hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_cast_actor", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			type probe struct { query string; args []any; null bool }
			probes := []probe{
				{"SELECT ('a=>1'::hstore)::jsonb AS j", nil, false},
				{"SELECT (NULL::hstore)::jsonb AS j", nil, true},
				{"SELECT v::jsonb AS j FROM hs_jsonb_cast_acl WHERE id=1", nil, false},
				{"SELECT v::jsonb AS j FROM hs_jsonb_cast_acl WHERE id=2", nil, false},
				{"SELECT ($1::hstore)::jsonb AS j", []any{"a=>1"}, false},
				{"SELECT ($1::hstore)::jsonb AS j", []any{nil}, true},
			}
			held := make([]*sql.Stmt, len(probes))
			for i, probe := range probes {
				var err error
				held[i], err = c.PrepareContext(p.Ctx, probe.query)
				if err != nil { t.Fatal(err) }
				defer held[i].Close()
			}
			hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION hstore_to_jsonb(hstore) FROM PUBLIC,hs_jsonb_cast_actor", 0))
			hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_cast_actor", 0))
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function hstore_to_jsonb")
			for i, probe := range probes {
				h := fixture.HeldStatement{Executor: c, Statement: held[i], SQL: probe.query}
				if probe.null {
					s := hstoreJSONBSelect(b, probe.query, []string{"j"}, [][]any{{nil}}, false, probe.args...)
					hstoreRun(t, p, b, c, s)
					hstoreRun(t, p, b, c, fixture.Prepared(s))
					hstoreRun(t, p, b, h, s)
				} else {
					hstoreError(t, p, b, c, probe.query, false, true, false, denied, probe.args...)
					hstoreError(t, p, b, c, probe.query, true, true, false, denied, probe.args...)
					hstoreError(t, p, b, h, probe.query, false, true, false, denied, probe.args...)
				}
			}
		})
	})
	t.Run("moved_schema_usage", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"hs_jsonb_schema_actor"})
		fixture.SchemaReferenceCleanup(t, "hs_jsonb_acl_schema")
		hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			for _, q := range []string{"CREATE ROLE hs_jsonb_schema_actor", "CREATE SCHEMA hs_jsonb_acl_schema", "ALTER FUNCTION hstore_to_jsonb(hstore) SET SCHEMA hs_jsonb_acl_schema", "GRANT USAGE ON SCHEMA public,hs_jsonb_acl_schema TO hs_jsonb_schema_actor", "SET ROLE hs_jsonb_schema_actor"} {
				hstoreRun(t, p, b, c, fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			q := "SELECT hs_jsonb_acl_schema.hstore_to_jsonb($1::hstore) AS j"
			s := hstoreJSONBSelect(b, q, []string{"j"}, [][]any{{`{"a": "1"}`}}, false, "a=>1")
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			hstoreRun(t, p, b, c, fixture.Exec("REVOKE USAGE ON SCHEMA hs_jsonb_acl_schema FROM hs_jsonb_schema_actor", 0))
			hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_schema_actor", 0))
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema hs_jsonb_acl_schema")
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, true, want, "a=>1")
			}
			hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			hstoreRun(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA hs_jsonb_acl_schema TO hs_jsonb_schema_actor", 0))
			hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_jsonb_schema_actor", 0))
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		})
	})
}
