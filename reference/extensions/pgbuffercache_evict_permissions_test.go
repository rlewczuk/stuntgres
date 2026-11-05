package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_evict_user", "bc_evict_member", "bc_evict_owner", "bc_evict_grantee", "bc_evict_super", "bc_evict_default", "bc_evict_public"})
	t.Run("current_admission", func(t *testing.T) {
		bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			bcDDL(t, p, b, c,
				"CREATE ROLE bc_evict_user", "CREATE ROLE bc_evict_member", "CREATE ROLE bc_evict_owner", "CREATE ROLE bc_evict_grantee", "CREATE ROLE bc_evict_super SUPERUSER",
				"GRANT pg_monitor TO bc_evict_member",
				"GRANT USAGE ON SCHEMA bc_part TO bc_evict_user,bc_evict_member,bc_evict_owner,bc_evict_grantee,bc_evict_super,pg_monitor")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			bceHeap(t, p, b, c, "permission_heap")
			bcDDL(t, p, b, c, "ALTER TABLE bc_part.permission_heap OWNER TO bc_evict_owner")
			before := bceWarm(t, p, b, c, "permission_heap")
			specs := []struct{ name, signature, call string }{
				{"pg_buffercache_evict", "pg_buffercache_evict(integer)", "pg_buffercache_evict(-1)"},
				{"pg_buffercache_evict_relation", "pg_buffercache_evict_relation(regclass)", "pg_buffercache_evict_relation(0::oid::regclass)"},
				{"pg_buffercache_evict_all", "pg_buffercache_evict_all()", "pg_buffercache_evict_all()"},
			}
			held := make([]fixture.HeldStatement, len(specs))
			for i, spec := range specs {
				q := "SELECT * FROM bc_part." + spec.call
				held[i] = bceHeld(t, p, c, q)
				bcBoth(t, p, b, c, fixture.Select("SELECT has_function_privilege('bc_evict_user',$1,'EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}, "bc_part."+spec.signature))
				bcDDL(t, p, b, c, "ALTER FUNCTION bc_part."+spec.signature+" OWNER TO bc_evict_owner", "GRANT EXECUTE ON FUNCTION bc_part."+spec.signature+" TO bc_evict_grantee")
			}
			for _, role := range []string{"bc_evict_user", "bc_evict_member", "bc_evict_owner", "bc_evict_grantee", "pg_monitor"} {
				t.Run(role, func(t *testing.T) {
					bcDDL(t, p, b, c, "SET ROLE "+role)
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					for i, spec := range specs {
						t.Run(spec.name, func(t *testing.T) {
							q := "SELECT * FROM bc_part." + spec.call
							s := bceRuntimeDenial(q, spec.name)
							bcFailure(t, p, b, c, s, "execute")
							bcRun(t, p, b, held[i], s)
						})
					}
					for _, spec := range []struct{ name, formal string; single bool }{
						{"pg_buffercache_evict", "integer", true}, {"pg_buffercache_evict_relation", "regclass", false},
					} {
						bcBoth(t, p, b, c, bceNullStep("SELECT * FROM bc_part."+spec.name+"($1::"+spec.formal+")", spec.single, nil))
					}
				})
			}
			bcDDL(t, p, b, c, "RESET ROLE")
			if after := bcTarget(t, p, b, c, "bc_part.permission_heap"); !b.PG && !reflect.DeepEqual(before, after) {
				t.Fatal("denied calls mutated occupancy", before, after)
			}
			t.Run("coercion_precedes_body_superuser_check", func(t *testing.T) {
				bcDDL(t, p, b, c, "SET ROLE bc_evict_user")
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				for _, tc := range []struct{ query, code, message string }{
					{"SELECT * FROM bc_part.pg_buffercache_evict('bad_integer')", "22P02", "invalid input syntax for type integer: \"bad_integer\""},
					{"SELECT * FROM bc_part.pg_buffercache_evict_relation('bc_part.absent_permission_heap')", "42P01", "relation \"bc_part.absent_permission_heap\" does not exist"},
				} {
					s := bcError(tc.query, tc.code, tc.message)
					s.Diagnostic.Position = fixture.Position(strings.Index(tc.query, "'") + 1)
					bcFailure(t, p, b, c, s, "prepare")
				}
			})
			t.Run("effective_superuser_regained", func(t *testing.T) {
				bcDDL(t, p, b, c, "SET ROLE bc_evict_super")
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				q := "SELECT * FROM bc_part.pg_buffercache_evict_relation($1::regclass)"
				h := bceHeld(t, p, c, q)
				bcDDL(t, p, b, c, "RESET ROLE", "SET ROLE bc_evict_user")
				s := bceRuntimeDenial(q, "pg_buffercache_evict_relation")
				s.Args = []any{"bc_part.permission_heap"}
				bcRun(t, p, b, h, s)
				bcDDL(t, p, b, c, "RESET ROLE", "SET ROLE bc_evict_super")
				bceCounts(t, b, bceCall(t, p, b, h, false, false, q, "bc_part.permission_heap"), bcCapacity(t, p, c), 1, 0)
			})
			bcDDL(t, p, b, c, "RESET ROLE")
			for i, spec := range specs {
				t.Run(spec.name+"_execute_acl", func(t *testing.T) {
					bcDDL(t, p, b, c, "REVOKE EXECUTE ON FUNCTION bc_part."+spec.signature+" FROM PUBLIC", "SET ROLE bc_evict_user")
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					q := "SELECT * FROM bc_part." + spec.call
					bcFailure(t, p, b, c, bcError(q, "42501", "permission denied for function "+spec.name), "execute")
					bcRun(t, p, b, held[i], bcError(q, "42501", "permission denied for function "+spec.name))
					bcDDL(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION bc_part."+spec.signature+" TO bc_evict_user", "SET ROLE bc_evict_user")
					bcFailure(t, p, b, c, bceRuntimeDenial(q, spec.name), "execute")
					bcRun(t, p, b, held[i], bceRuntimeDenial(q, spec.name))
				})
			}
			bcDDL(t, p, b, c, "RESET ROLE")
			t.Run("strict_null_versus_execute_acl", func(t *testing.T) {
				for _, spec := range []struct{ name, formal string; single bool }{
					{"pg_buffercache_evict", "integer", true}, {"pg_buffercache_evict_relation", "regclass", false},
				} {
					t.Run(spec.name, func(t *testing.T) {
						signature := "bc_part." + spec.name + "(" + spec.formal + ")"
						bcDDL(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM bc_evict_user", "SET ROLE bc_evict_user")
						defer func() {
							bcDDL(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION "+signature+" TO bc_evict_user")
						}()
						// Pinned 18.6 denies even a literal-NULL FROM call:
						// strictness bypasses the body, not SQL EXECUTE admission.
						for _, q := range []string{
							"SELECT * FROM bc_part." + spec.name + "(NULL::" + spec.formal + ")",
							"SELECT * FROM bc_part." + spec.name + "($1::" + spec.formal + ")",
							"SELECT e.* FROM (VALUES(NULL::" + spec.formal + ")) v(x),bc_part." + spec.name + "(v.x) e",
						} {
							s := bcError(q, "42501", "permission denied for function "+spec.name)
							if strings.Contains(q, "$1") {
								s.Args = []any{nil}
							}
							bcFailure(t, p, b, c, s, "execute")
						}
					})
				}
			})
			t.Run("namespace_lookup", func(t *testing.T) {
				bcDDL(t, p, b, c, "SET ROLE bc_evict_user")
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				q := "SELECT * FROM bc_part.pg_buffercache_evict(-1)"
				h := bceHeld(t, p, c, q)
				bcDDL(t, p, b, c, "RESET ROLE", "REVOKE USAGE ON SCHEMA bc_part FROM bc_evict_user", "SET ROLE bc_evict_user")
				s := bcError(q, "42501", "permission denied for schema bc_part")
				s.Diagnostic.Position = fixture.Position(strings.Index(q, "bc_part") + 1)
				bcFailure(t, p, b, c, s, "prepare")
				bcRun(t, p, b, h, s)
				bcDDL(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA bc_part TO bc_evict_user", "SET ROLE bc_evict_user")
				bcRun(t, p, b, h, bceRuntimeDenial(q, "pg_buffercache_evict"))
			})
			bcDDL(t, p, b, c, "RESET ROLE")
			bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.permission_heap")
		})
	})
	t.Run("ordinary_default_privileges", func(t *testing.T) {
		bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			bcDDL(t, p, b, c, "CREATE ROLE bc_evict_default", "CREATE ROLE bc_evict_public", "GRANT USAGE ON SCHEMA bc_part TO bc_evict_default",
				"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC",
				"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT EXECUTE ON FUNCTIONS TO bc_evict_default",
				"CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			defer c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
			for _, signature := range []string{"pg_buffercache_evict(integer)", "pg_buffercache_evict_relation(regclass)", "pg_buffercache_evict_all()"} {
				bcBoth(t, p, b, c, fixture.Select("SELECT has_function_privilege('bc_evict_default',$1,'EXECUTE') AS allowed,has_function_privilege('bc_evict_public',$1,'EXECUTE') AS public", []string{"allowed", "public"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, "bc_part."+signature))
			}
			bcDDL(t, p, b, c, "SET ROLE bc_evict_default")
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			bcFailure(t, p, b, c, bceRuntimeDenial("SELECT * FROM bc_part.pg_buffercache_evict_all()", "pg_buffercache_evict_all"), "execute")
			bcDDL(t, p, b, c, "RESET ROLE", "ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part REVOKE EXECUTE ON FUNCTIONS FROM bc_evict_default", "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
		})
	})
}
