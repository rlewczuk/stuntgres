package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessSpecialRestore(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		admin := "postgres"
		if b.PG {
			admin = "stuntgres"
		}
		run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_other WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER lc_messages TO og_object_reader", 0))
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO og_object_reader WITH GRANT OPTION", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT SELECT ON LARGE OBJECT 910201 TO PUBLIC", 0)) })
		run(fixture.Exec("GRANT SET ON PARAMETER \"special.custom\" TO PUBLIC", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER work_mem TO og_object_reader", 0))
		run(fixture.Exec("REVOKE ALL ON PARAMETER work_mem FROM CURRENT_USER", 0))
		run(fixture.Exec("REVOKE ALL ON PARAMETER work_mem FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_create(910202::oid)", "OID", int64(910202)))
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910202 FROM og_object_other", 0))
			run(largeObjectValue("lo_create(910203::oid)", "OID", int64(910203)))
		})
		verify := func() {
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','LC_MESSAGES','SET') AS v", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','lc_messages','ALTER SYSTEM') AS v", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','lc_messages','ALTER SYSTEM WITH GRANT OPTION') AS v", false))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_other','lc_messages','ALTER SYSTEM WITH GRANT OPTION') AS v", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','special.custom','SET') AS v", true))
			run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','work_mem','SET,ALTER SYSTEM') AS v", false))
			run(fixture.Select("SELECT parname,paracl::text AS acl FROM pg_parameter_acl WHERE parname IN ('lc_messages','special.custom','work_mem') ORDER BY parname", []string{"parname", "acl"}, []string{"TEXT", "TEXT"}, [][]any{
				{"lc_messages", "{" + admin + "=sA/" + admin + ",og_object_reader=s/" + admin + ",og_object_other=A*/" + admin + ",og_object_reader=A/og_object_other}"},
				{"special.custom", "{" + admin + "=sA/" + admin + ",=s/" + admin + "}"},
				{"work_mem", "{}"},
			}))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_reader',910201::oid,'SELECT WITH GRANT OPTION') AS v", true))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege('public',910201::oid,'SELECT') AS v", true))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_other',910202::oid,'SELECT') AS v", false))
			run(objectGrantAccessValue("SELECT has_largeobject_privilege('og_object_other',910202::oid,'SELECT WITH GRANT OPTION') AS v", true))
			run(fixture.Select("SELECT oid,lomowner::regrole::text AS owner,lomacl::text AS acl FROM pg_largeobject_metadata WHERE oid IN (910201,910202,910203) ORDER BY oid", []string{"oid", "owner", "acl"}, []string{"OID", "TEXT", "TEXT"}, [][]any{{int64(910201), "og_object_other", "{og_object_other=rw/og_object_other,og_object_reader=r*/og_object_other,=r/og_object_reader}"}, {int64(910202), "og_object_other", "{}"}, {int64(910203), "og_object_other", nil}}))
			run(largeObjectBytes(910201, "00ff61"))
		}
		verify()
		if b.PG {
			return
		} // Reset/Dump/Restore are explicitly native controls.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM og_object_reader", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER application_name TO og_object_reader", 0))
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ROLLBACK", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify()
		run(objectGrantAccessValue("SELECT has_parameter_privilege('og_object_reader','application_name','SET') AS v", false))
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(snapshot.Bytes(), again.Bytes()) {
			t.Fatal("parameter and LO raw authority redump differs", err)
		}
		run(largeObjectValue("lo_from_bytea(910204::oid,decode('abcd','hex'))", "OID", int64(910204)))
		for _, invalid := range []string{
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{special_missing=s/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{og_object_reader=s/special_missing}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{og_object_reader=r/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text OR true;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[],parname='other' WHERE parname='lc_messages'::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname=lower('lc_messages');",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname=$1::pg_catalog.text;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text RETURNING parname;",
			"UPDATE pg_catalog.pg_parameter_acl SET paracl='{og_object_reader=z/postgres}'::pg_catalog.aclitem[] WHERE parname='lc_messages'::pg_catalog.text;",
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(snapshot.String()+"\n"+invalid)); err == nil {
				t.Fatal("invalid parameter repair admitted", invalid)
			}
			verify()
			run(largeObjectBytes(910204, "abcd"))
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(snapshot.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Restore", err)
		}
		verify()
		run(largeObjectBytes(910204, "abcd"))
	})
}
