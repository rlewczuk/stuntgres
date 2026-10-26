package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessSpecialPrivileges(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, family := range []struct{ fn, target, yes, no string }{{"has_largeobject_privilege", "910201::oid", "SELECT", "UPDATE"}, {"has_parameter_privilege", "'lc_messages'::text", "SET", "ALTER SYSTEM"}} {
			q := "SELECT " + family.fn + "('og_object_reader'," + family.target + ",$1::text) AS v"
			for _, tc := range []struct {
				priv string
				want bool
			}{{family.yes, true}, {family.no, false}, {strings.ToLower(family.yes), true}, {" \t" + family.yes + "\n ", true}, {family.yes + "," + family.no, true}, {family.yes + " WITH GRANT OPTION", false}, {family.no + " WITH GRANT OPTION", false}, {family.no + "," + family.yes + " WITH GRANT OPTION", false}} {
				run(objectGrantAccessValue(q, tc.want, tc.priv))
			}
			for _, bad := range []string{"", "ALL", ",", family.yes + ",", "," + family.yes, family.yes + ",bad", family.yes + "  WITH GRANT OPTION", family.yes + "\tWITH GRANT OPTION", family.yes + " WITH GRANT OPTION bad"} {
				offending := bad
				if i := strings.IndexByte(bad, ','); i >= 0 {
					if i == 0 {
						offending = ""
					} else {
						offending = strings.TrimSpace(bad[i+1:])
					}
				}
				run(accessExecutionError(q, "22023", `unrecognized privilege type: "`+offending+`"`, bad))
			}
		}
		run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT 910201 TO og_object_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_reader WITH GRANT OPTION", 0))
		run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_reader',910201::oid,'UPDATE WITH GRANT OPTION') AS v", true))
		run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','lc_messages','ALTER SYSTEM WITH GRANT OPTION') AS v", true))
		run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO PUBLIC", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO PUBLIC", 0))
		for _, actor := range []string{"'public'::name", "4294967294::oid"} {
			run(objectGrantAccessValue("SELECT has_largeobject_privilege("+actor+",910201::oid,'SELECT') AS v", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege("+actor+",'lc_messages','SET') AS v", true))
		}
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910201 FROM PUBLIC,og_object_other", 0))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege(910201::oid,'SELECT') AS v", false))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege(910201::oid,'SELECT WITH GRANT OPTION') AS v", true))
		})
		run(fixture.Exec("GRANT SET ON PARAMETER work_mem TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			for _, name := range []string{"work_mem", "WORK_MEM", "SORT_MEM"} {
				run(objectGrantAccessValue("SELECT has_parameter_privilege($1::text,'SET') AS v", true, name))
			}
			for _, name := range []string{"", "special_missing", "application_name", "unregistered.custom"} {
				run(objectGrantAccessValue("SELECT has_parameter_privilege($1::text,'SET') AS v", false, name))
			}
			run(fixture.Exec("SET application_name='special ACL witness'", 0))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('application_name','SET') AS v", false))
		})
		for _, name := range []string{"", "special_missing", "unregistered.custom"} {
			run(objectGrantAccessValue("SELECT has_parameter_privilege($1::text,'SET') AS v", true, name))
		}
		for _, compat := range []string{"off", "on"} {
			run(fixture.Exec("SET lo_compat_privileges="+compat, 0))
			for _, actor := range []string{"current_user", "'og_object_reader'::name"} {
				for _, oid := range []string{"0", "4294967294"} {
					run(objectGrantAccessValue("SELECT has_largeobject_privilege("+actor+","+oid+"::oid,'SELECT') AS v", nil))
				}
			}
			run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_other',910201::oid,'UPDATE') AS v", compat == "on"))
		}
		run(fixture.Exec("RESET lo_compat_privileges", 0))
		run(fixture.Exec("GRANT pg_read_all_data,pg_write_all_data TO og_object_other", 0))
		run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_other',910201::oid,'SELECT,UPDATE') AS v", false))
	})
}
