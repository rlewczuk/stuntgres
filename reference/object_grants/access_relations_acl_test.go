package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const accessRelationsVector = "SELECT has_column_privilege('ar_reader','ar_t','a','SELECT') AS a,has_column_privilege('ar_reader','ar_t','b','SELECT') AS b,has_any_column_privilege('ar_reader','ar_t','SELECT') AS any,has_table_privilege('ar_reader','ar_t','SELECT') AS tab"

func accessRelationsVectorStep(a, b, anyColumn, table bool) fixture.Step {
	return fixture.Select(accessRelationsVector, []string{"a", "b", "any", "tab"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{a, b, anyColumn, table}})
}
func accessRelationsColumns(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	held := accessExecutionHeld(t, p, c, accessRelationsVector)
	check := func(a, bb, any, table bool) {
		s := accessRelationsVectorStep(a, bb, any, table)
		accessRelationsRoutes(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	check(false, false, false, false)
	run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader", 0))
	check(true, false, true, false)
	fixture.RoleGrantActor(run, "ar_reader", func() {
		run(fixture.Select("SELECT a FROM ar_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(accessRelationsError("SELECT b FROM ar_t", "42501", "permission denied for table ar_t"))
	})
	run(fixture.Exec("GRANT SELECT(b) ON ar_t TO ar_reader", 0))
	check(true, true, true, false)
	run(fixture.Exec("REVOKE SELECT(a),SELECT(b) ON ar_t FROM ar_reader", 0))
	check(false, false, false, false)
	run(fixture.Exec("GRANT SELECT(a) ON ar_t TO ar_reader WITH GRANT OPTION", 0))
	option := strings.ReplaceAll(accessRelationsVector, "'SELECT'", "'SELECT WITH GRANT OPTION'")
	run(fixture.Select(option, []string{"a", "b", "any", "tab"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, false}}))
	run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(a) ON ar_t FROM ar_reader", 0))
	check(true, false, true, false)
	run(fixture.Exec("GRANT SELECT ON ar_t TO ar_reader", 0))
	run(fixture.Exec("REVOKE SELECT(a) ON ar_t FROM ar_reader", 0))
	check(true, true, true, true)
}
func TestReferenceAccessRelationsColumns(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		accessRelationsColumns(t, p, b, c, run)
	})
}

func TestReferenceAccessRelationsPrivilegeTokens(t *testing.T) {
	for _, family := range []struct {
		name, object string
		tokens       []string
	}{
		{"has_table_privilege", "ar_t", []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"}},
		{"has_column_privilege", "ar_t", []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}},
		{"has_any_column_privilege", "ar_t", []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}},
		{"has_sequence_privilege", "ar_s", []string{"USAGE", "SELECT", "UPDATE"}},
	} {
		for _, token := range family.tokens {
			t.Run(family.name+"/"+token, func(t *testing.T) {
				accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					column := ""
					grant := token
					if family.name == "has_column_privilege" || family.name == "has_any_column_privilege" {
						grant += "(a)"
					}
					if family.name == "has_column_privilege" {
						column = ",'a'"
					}
					q := "SELECT " + family.name + "('ar_reader','" + family.object + "'" + column + ",$1::text) AS v"
					accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(q, false, token))
					run(fixture.Exec("GRANT "+grant+" ON "+family.object+" TO ar_reader", 0))
					accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(q, true, " \t"+strings.ToLower(token)+" \n"))
					run(objectGrantAccessValue(q, false, token+" WITH GRANT OPTION"))
					run(fixture.Exec("GRANT "+grant+" ON "+family.object+" TO ar_reader WITH GRANT OPTION", 0))
					accessRelationsRoutes(t, p, b, c, objectGrantAccessValue(q, true, token+" WITH GRANT OPTION"))
					run(objectGrantAccessValue(q, true, "UPDATE,"+token+" WITH GRANT OPTION"))
					for _, bad := range []string{"", "ALL", "bad", token + ",bad", token + ",", token + "  WITH GRANT OPTION", token + "\tWITH GRANT OPTION"} {
						offending := bad
						if i := strings.IndexByte(bad, ','); i >= 0 {
							offending = bad[i+1:]
						}
						accessRelationsRoutes(t, p, b, c, accessRelationsError(q, "22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, offending), bad))
					}
				})
			})
		}
	}
}
func TestReferenceAccessRelationsRoles(t *testing.T) {
	for _, phase := range []string{"inherit", "owner", "public", "predefined"} {
		t.Run(phase, func(t *testing.T) {
			accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				q := "SELECT has_table_privilege($1::name,'ar_t',$2::text) AS v"
				held := accessExecutionHeld(t, p, c, q)
				check := func(role, priv string, want bool) {
					s := objectGrantAccessValue(q, want, role, priv)
					accessRelationsRoutes(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				switch phase {
				case "inherit":
					run(fixture.Exec("GRANT SELECT ON ar_t TO ar_group", 0))
					run(fixture.Exec("GRANT ar_group TO ar_reader WITH INHERIT FALSE, SET TRUE", 0))
					check("ar_reader", "SELECT", false)
					run(fixture.Exec("GRANT ar_group TO ar_reader WITH INHERIT TRUE", 0))
					check("ar_reader", "SELECT", true)
					run(fixture.Exec("REVOKE ar_group FROM ar_reader", 0))
					check("ar_reader", "SELECT", false)
					run(fixture.Exec("GRANT ar_group TO ar_set WITH INHERIT FALSE", 0))
					run(fixture.Exec("GRANT ar_set TO ar_reader WITH INHERIT TRUE", 0))
					check("ar_reader", "SELECT", false)
				case "owner":
					run(fixture.Exec("REVOKE ALL ON ar_t FROM ar_owner", 0))
					check("ar_owner", "SELECT", false)
					check("ar_owner", "SELECT WITH GRANT OPTION", true)
					run(fixture.Exec("GRANT ar_owner TO ar_reader", 0))
					check("ar_reader", "SELECT WITH GRANT OPTION", true)
				case "public":
					run(fixture.Exec("GRANT SELECT ON ar_t TO PUBLIC", 0))
					check("public", "SELECT", true)
					check("ar_reader", "SELECT", true)
					run(objectGrantAccessValue("SELECT has_table_privilege(4294967294::oid,'ar_t','SELECT') AS v", true))
					run(accessRelationsError(q, "42704", `role "PUBLIC" does not exist`, "PUBLIC", "SELECT"))
					fixture.RoleGrantActor(run, "ar_reader", func() { run(objectGrantAccessValue("SELECT has_table_privilege('ar_t','SELECT') AS v", true)) })
				case "predefined":
					for _, tc := range []struct{ role, priv string }{{"pg_read_all_data", "SELECT"}, {"pg_write_all_data", "INSERT"}, {"pg_write_all_data", "UPDATE"}, {"pg_write_all_data", "DELETE"}, {"pg_maintain", "MAINTAIN"}} {
						run(fixture.Exec("GRANT "+tc.role+" TO ar_reader", 0))
						check("ar_reader", tc.priv, true)
						check("ar_reader", tc.priv+" WITH GRANT OPTION", false)
						run(fixture.Exec("REVOKE "+tc.role+" FROM ar_reader", 0))
					}
				}
			})
		})
	}
}
func TestReferenceAccessRelationsSequence(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		q := "SELECT has_sequence_privilege('ar_reader','ar_s','USAGE') AS u,has_sequence_privilege('ar_reader','ar_s','SELECT') AS s,has_sequence_privilege('ar_reader','ar_s','UPDATE') AS w"
		check := func(u, s, w bool) {
			accessRelationsRoutes(t, p, b, c, fixture.Select(q, []string{"u", "s", "w"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{u, s, w}}))
		}
		check(false, false, false)
		run(fixture.Exec("GRANT INSERT ON ar_t TO ar_reader", 0))
		check(false, false, false)
		run(fixture.Exec("GRANT SELECT ON SEQUENCE ar_s TO ar_reader", 0))
		check(false, true, false)
		run(fixture.Exec("GRANT UPDATE ON SEQUENCE ar_s TO ar_reader", 0))
		check(false, true, true)
		run(fixture.Exec("GRANT USAGE ON SEQUENCE ar_s TO ar_reader", 0))
		check(true, true, true)
		run(fixture.Select("SELECT last_value,is_called FROM ar_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(17), false}}))
		for _, priv := range []string{"USAGE", "SELECT", "UPDATE"} {
			run(objectGrantAccessValue("SELECT has_sequence_privilege('ar_owner','ar_s',$1::text) AS v", true, priv))
		}
		run(objectGrantAccessValue("SELECT has_table_privilege('ar_owner','ar_s','DELETE') AS v", false))
		run(objectGrantAccessValue("SELECT has_table_privilege('ar_owner','ar_s','DELETE WITH GRANT OPTION') AS v", true))
		run(objectGrantAccessValue("SELECT has_table_privilege(current_user,'ar_s','DELETE') AS v", true))
		run(accessRelationsError("SELECT has_sequence_privilege('ar_reader','ar_t','SELECT')", "42809", `"ar_t" is not a sequence`))
	})
}
