package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"copy_filter_reader"})
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE ROLE copy_filter_reader")
		c.exec(t, "GRANT USAGE ON SCHEMA public TO copy_filter_reader")
		c.exec(t, "CREATE TABLE copy_acl(id int,keep bool DEFAULT true)")
		c.exec(t, "INSERT INTO copy_acl VALUES(99,true)")
		c.exec(t, "GRANT INSERT(id) ON copy_acl TO copy_filter_reader")
		c.exec(t, "CREATE FUNCTION copy_acl_allow(int) RETURNS boolean LANGUAGE SQL AS 'SELECT $1>0'")
		c.exec(t, "REVOKE ALL ON FUNCTION copy_acl_allow(int) FROM PUBLIC")
		c.exec(t, "SET ROLE copy_filter_reader")
		defer c.exec(t, "RESET ROLE")
		copyFilterImport(t, c, "COPY copy_acl(id) FROM STDIN WHERE TRUE", "1\n", 1)
		copyFilterFailure(t, c, "COPY copy_acl(id) FROM STDIN WHERE keep", "2\n", fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_acl"})
		copyFilterFailure(t, c, "COPY copy_acl(id) FROM STDIN WITH (ON_ERROR bogus) WHERE keep", "2\n", fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_acl"})
		c.exec(t, "RESET ROLE")
		copyFilterIDs(t, c, "copy_acl", 1, 99)
		c.exec(t, "GRANT SELECT(keep,id) ON copy_acl TO copy_filter_reader")
		c.exec(t, "SET ROLE copy_filter_reader")
		copyFilterImport(t, c, "COPY copy_acl(id) FROM STDIN WHERE copy_acl.keep", "2\n", 1)
		copyFilterFailure(t, c, "COPY copy_acl(id) FROM STDIN WHERE copy_acl_allow(id)", "3\n", fixture.Diagnostic{Code: "42501", Message: "permission denied for function copy_acl_allow"})
		c.exec(t, "RESET ROLE")
		copyFilterIDs(t, c, "copy_acl", 1, 2, 99)
		c.exec(t, "GRANT EXECUTE ON FUNCTION copy_acl_allow(int) TO copy_filter_reader")
		c.exec(t, "SET ROLE copy_filter_reader")
		copyFilterImport(t, c, "COPY copy_acl(id) FROM STDIN WHERE copy_acl_allow(id)", "3\n-1\n", 1)
		c.exec(t, "RESET ROLE")
		copyFilterIDs(t, c, "copy_acl", 1, 2, 3, 99)
		copyFilterNotices(t, *notices)
	})
}
