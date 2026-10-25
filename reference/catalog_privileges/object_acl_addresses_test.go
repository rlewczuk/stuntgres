package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLAddresses(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "acl_obj_address_schema")
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_addresses(a INT,b INT)", 0))
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_addresses TO PUBLIC", 0))
		for _, subid := range []int{1, 65537, -65535} {
			run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_addresses'::regclass,"+strconv.Itoa(subid)+") AS acl", "{=r/"+objectACLOwner(b)+"}"))
		}
		for _, subid := range []int{0, 2, 3, -1, -2, -6, 32767, 32768, 65535, 65536, -65536, 2147483647, -2147483648} {
			run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_addresses'::regclass,"+strconv.Itoa(subid)+") AS acl", nil))
		}
		run(fixture.Exec("ALTER TABLE acl_obj_addresses RENAME COLUMN a TO old_a", 0))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_addresses'::regclass,1) AS acl", "{=r/"+objectACLOwner(b)+"}"))
		run(fixture.Exec("ALTER TABLE acl_obj_addresses DROP COLUMN old_a", 0))
		run(fixture.Exec("ALTER TABLE acl_obj_addresses ADD COLUMN a INT", 0))
		for _, subid := range []string{"1", "2", "3", "65537"} {
			run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_addresses'::regclass,"+subid+") AS acl", nil))
		}
		// Each ACL-bearing class, including unavailable native providers, must
		// distinguish a missing object from an unrecognized class.
		for _, class := range []int{1259, 2615, 1247, 1255, 1262, 2612, 1213, 2328, 1417, 2995, 2613} {
			for _, subid := range []string{"0", "1", "-1"} {
				run(objectACLSelect("SELECT pg_get_acl("+strconv.Itoa(class)+",0,"+subid+") AS acl", nil))
			}
		}
		for _, class := range []int{3456, 2607, 1260, 826} {
			for _, object := range []string{"0", "999999"} {
				run(objectACLSelect("SELECT pg_get_acl("+strconv.Itoa(class)+","+object+",1) AS acl", nil))
			}
		}
		for _, subid := range []string{"0", "1", "-1"} {
			run(objectACLSelect("SELECT pg_get_acl(0,0,"+subid+") AS acl", nil))
		}
		run(fixture.Exec("CREATE SCHEMA acl_obj_address_schema", 0))
		for _, subid := range []string{"0", "1", "-1", "65537"} {
			run(objectACLSelect("SELECT pg_get_acl(2615,(SELECT oid FROM pg_namespace WHERE nspname='acl_obj_address_schema'),"+subid+") AS acl", nil))
		}
		run(fixture.Exec("CREATE SEQUENCE acl_obj_address_seq", 0))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_address_seq'::regclass,0) AS acl", nil))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE acl_obj_address_seq TO PUBLIC", 0))
		s := fixture.Select("SELECT unnest(pg_get_acl(1259,'acl_obj_address_seq'::regclass,0)) AS acl", []string{"acl"}, []string{"ACLITEM"}, [][]any{{objectACLOwner(b) + "=rwU/" + objectACLOwner(b)}, {"=U/" + objectACLOwner(b)}})
		s.Bag = true
		run(s)
		run(objectACLSelect("SELECT pg_get_acl(1255,'pg_catalog.pg_get_acl(oid,oid,integer)'::regprocedure,0) AS acl", nil))
		run(objectACLSelect("SELECT pg_get_acl(1213,1663,0) AS acl", nil))
	})
}

func TestReferenceObjectACLDatabase(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, subid := range []string{"0", "1", "-1"} {
			objectACLRun(t, p, b, c, objectACLSelect("SELECT pg_get_acl(1262,(SELECT oid FROM pg_database WHERE datname=current_database()),"+subid+") AS acl", nil))
		}
		objectACLRun(t, p, b, c, objectACLSelect("SELECT datacl AS acl FROM pg_database WHERE datname=current_database()", nil))
		// Consistency supplements, never replaces, the independent expectations.
		objectACLRun(t, p, b, c, fixture.Select("SELECT pg_get_acl(1262,oid,0) IS NOT DISTINCT FROM datacl AS same FROM pg_database WHERE datname=current_database()", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
