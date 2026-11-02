package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pkey_reader"})
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE pkey_reader",
			"CREATE TABLE pkey_secret(a integer,b integer,PRIMARY KEY(b,a))",
			"CREATE TABLE pkey_no_key_secret(a integer,b integer)",
			"CREATE SCHEMA pk_hidden",
			"CREATE TABLE pk_hidden.pkey_hidden(id integer PRIMARY KEY)",
			"GRANT USAGE ON SCHEMA public,pk_ext TO pkey_reader",
			"GRANT SELECT ON pk_hidden.pkey_hidden TO pkey_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		held := pkeyHeld(t, p, c, pkeyCall)
		check := func(name string, s fixture.Step) {
			t.Run(name, func(t *testing.T) { pkeyRoutes(t, p, b, c, &held, s) })
		}
		denied := pkeyError(pkeyCall, "42501", "permission denied for table pkey_secret", "pkey_secret")
		good := pkeyRows([][]any{{int64(1), "b"}, {int64(2), "a"}}, "pkey_secret")
		for _, phase := range []struct{ name, grant string }{
			{"no_select", ""},
			{"one_column_select", "GRANT SELECT(a) ON pkey_secret TO pkey_reader"},
			{"all_columns_select", "GRANT SELECT(b) ON pkey_secret TO pkey_reader"},
		} {
			if phase.grant != "" {
				run(fixture.Exec(phase.grant, 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
			t.Run(phase.name+"_privilege_witness", func(t *testing.T) {
				s := fixture.Select("SELECT has_table_privilege(current_user,'pkey_secret','SELECT') AS table_select,has_column_privilege(current_user,'pkey_secret','a','SELECT') AS a_select,has_column_privilege(current_user,'pkey_secret','b','SELECT') AS b_select", []string{"table_select", "a_select", "b_select"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, phase.name != "no_select", phase.name == "all_columns_select"}})
				pkeyRoutes(t, p, b, c, nil, s)
			})
			check(phase.name, denied)
			check(phase.name+"_null", pkeyRows(nil, nil))
			check(phase.name+"_no_key", pkeyError(pkeyCall, "42501", "permission denied for table pkey_no_key_secret", "pkey_no_key_secret"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT ON pkey_secret,pkey_no_key_secret TO pkey_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
		check("table_select", good)
		check("table_select_no_key", pkeyRows(nil, "pkey_no_key_secret"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON pkey_secret FROM pkey_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
		check("table_select_revoked", denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))

		// A target schema is resolved using the caller's privileges, not the
		// installed function's schema or owner. Table SELECT cannot bypass USAGE.
		run(fixture.Exec("SET search_path=pk_hidden,public", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
		check("qualified_schema_denied", pkeyError(pkeyCall, "42501", "permission denied for schema pk_hidden", "pk_hidden.pkey_hidden"))
		check("invisible_path_omitted", pkeyError(pkeyCall, "42P01", `relation "pkey_hidden" does not exist`, "pkey_hidden"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA pk_hidden TO pkey_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
		check("schema_usage_granted", pkeyRows([][]any{{int64(1), "id"}}, "pkey_hidden"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET search_path=public", 0))

		for _, q := range []string{
			"GRANT SELECT ON pkey_secret TO pkey_reader",
			"REVOKE EXECUTE ON FUNCTION pk_ext.dblink_get_pkey(text) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION pk_ext.dblink_get_pkey(text) TO pkey_reader",
			"SET SESSION AUTHORIZATION pkey_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		// Prepare while allowed, then use the same statements across revoke /
		// grant / revoke. Both parameter NULL and literal NULL exercise STRICT
		// without granting stale routine authority or resolving a target name.
		readerHeld := pkeyHeld(t, p, c, pkeyCall)
		const literalNull = "SELECT * FROM pk_ext.dblink_get_pkey(NULL::text) ORDER BY position"
		nullHeld := pkeyHeld(t, p, c, literalNull)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, phase := range []struct {
			name    string
			allowed bool
		}{
			{"execute_revoked", false}, {"execute_regranted", true}, {"execute_revoked_again", false},
		} {
			q := "REVOKE EXECUTE ON FUNCTION pk_ext.dblink_get_pkey(text) FROM pkey_reader"
			if phase.allowed {
				q = "GRANT EXECUTE ON FUNCTION pk_ext.dblink_get_pkey(text) TO pkey_reader"
			}
			run(fixture.Exec(q, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
			t.Run(phase.name, func(t *testing.T) {
				for _, tc := range []struct {
					name string
					arg  any
				}{{"nonnull", "pkey_secret"}, {"parameter_null", nil}} {
					t.Run(tc.name, func(t *testing.T) {
						s := pkeyError(pkeyCall, "42501", "permission denied for function dblink_get_pkey", tc.arg)
						if phase.allowed {
							s = good
							if tc.arg == nil {
								s = pkeyRows(nil, nil)
							}
						}
						pkeyRoutes(t, p, b, c, &readerHeld, s)
					})
				}
				t.Run("literal_null", func(t *testing.T) {
					s := pkeyError(literalNull, "42501", "permission denied for function dblink_get_pkey")
					if phase.allowed {
						s = fixture.Select(literalNull, []string{"position", "colname"}, []string{"INT4", "TEXT"}, nil)
					}
					pkeyRoutes(t, p, b, c, &nullHeld, s)
				})
			})
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pk_ext.dblink_get_pkey(text) TO pkey_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA pk_ext FROM pkey_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pkey_reader", 0))
		t.Run("routine_schema_denied", func(t *testing.T) {
			for _, route := range []string{"direct", "prepared", "held"} {
				t.Run(route, func(t *testing.T) {
					s := pkeyError(pkeyCall, "42501", "permission denied for schema pk_ext", "pkey_secret")
					s.Diagnostic.Position = fixture.Position(15)
					if route == "prepared" {
						s.Prepared, s.ErrorStage = true, "prepare"
					}
					if route == "held" {
						fixture.RunStep(t, p.Ctx, readerHeld, b.Recorder, s)
					} else {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				})
			}
		})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
