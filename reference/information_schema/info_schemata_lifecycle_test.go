package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSchemataLifecycle(t *testing.T) {
	iscScenario(t, func(c *iscCase) {
		c.exec("CREATE ROLE isc_owner", "CREATE ROLE isc_other_owner", "CREATE SCHEMA isc_owned AUTHORIZATION isc_owner")
		q := iscQuery + " WHERE schema_name=$1"
		held := c.hold(q)
		check := func(name, owner string) {
			var rows [][]any
			if owner != "" {
				rows = c.row(name, owner)
			}
			c.check(held, c.projection(q, rows, name))
		}
		oid := func(name string) uint64 {
			var id uint64
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=$1", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id == 0 {
				t.Fatal("zero namespace OID")
			}
			return id
		}
		check("isc_owned", "isc_owner")
		check("isc_owned", "isc_owner")
		original := oid("isc_owned")
		var notices []fixture.Diagnostic
		_, noticeConn, record := fixture.NoticeReferencePool(t, c.p, c.b.PG, &notices)
		fixture.RunStep(t, c.p.Ctx, noticeConn, record, fixture.Exec("CREATE SCHEMA IF NOT EXISTS isc_owned", 0))
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: `schema "isc_owned" already exists, skipping`}
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{wantNotice}) {
			t.Fatalf("notices=%#v", notices)
		}
		if err := noticeConn.Close(); err != nil {
			t.Fatal(err)
		}
		check("isc_owned", "isc_owner")
		c.exec("ALTER SCHEMA isc_owned RENAME TO isc_renamed")
		if oid("isc_renamed") != original {
			t.Fatal("rename replaced identity")
		}
		check("isc_owned", "")
		check("isc_renamed", "isc_owner")
		c.exec("ALTER SCHEMA isc_renamed OWNER TO isc_other_owner")
		check("isc_renamed", "isc_other_owner")
		c.exec("DROP SCHEMA isc_renamed")
		check("isc_renamed", "")
		c.exec("CREATE SCHEMA isc_owned AUTHORIZATION isc_owner")
		if oid("isc_owned") == original {
			t.Fatal("recreated schema reused identity")
		}
		check("isc_owned", "isc_owner")
		c.exec(`SET search_path=isc_absent_path,"$user",public`)
		for _, name := range []string{"isc_absent_path", "$user", "UTF8", "UCS"} {
			check(name, "")
		}
	})
}
