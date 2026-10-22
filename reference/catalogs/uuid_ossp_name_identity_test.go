package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func uuidNameInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	seen := map[int64]bool{}
	for _, f := range uuidNameFunctions {
		q := "SELECT p.pronargs,p.proargnames::text AS names,p.prorettype::bigint AS result,p.provolatile,p.proisstrict,p.proparallel,p.prokind,p.oid::regprocedure::text AS signature FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname='" + f.name + "'"
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, []string{"pronargs", "names", "result", "provolatile", "proisstrict", "proparallel", "prokind", "signature"}, []string{"INT2", "TEXT", "INT8", "CHAR", "BOOL", "CHAR", "CHAR", "TEXT"}, [][]any{{int64(2), "{namespace,name}", int64(2950), "i", true, "s", "f", "uuid_ossp_part." + f.name + "(uuid,text)"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		var oid, ns, owner, ext, count int64
		if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,p.pronamespace,p.proowner,e.oid FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='uuid-ossp' AND p.proname=$1", f.name).Scan(&oid, &ns, &owner, &ext); err != nil || oid <= 0 || ns <= 0 || owner <= 0 || ext <= 0 || seen[oid] {
			t.Fatal("installed identity", oid, ns, owner, ext, err)
		}
		seen[oid] = true
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", oid, ext).Scan(&count); err != nil || count != 1 {
			t.Fatal("member link", count, err)
		}
	}
}

func TestReferenceUUIDOsspNameIdentity(t *testing.T) {
	uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, f := range uuidNameFunctions {
			uuidOsspFailure(t, p, c, "SELECT "+f.name+"(NULL::uuid,NULL::text)", uuidOsspMissing(f.name+"(uuid, text)", 8))
		}
		run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("ROLLBACK", 0))
		for _, f := range uuidNameFunctions {
			uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part."+f.name+"(NULL::uuid,NULL::text)", uuidOsspMissing("uuid_ossp_part."+f.name+"(uuid, text)", 8))
		}
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		uuidNameInventory(t, p, c, r)
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}))
		}
		for _, f := range uuidNameFunctions {
			q := "SELECT " + f.name + "('" + uuidNameDNS + "','www.example.com') AS u"
			uuidOsspFailure(t, p, c, q, uuidOsspMissing(f.name+"(unknown, unknown)", 8))
			run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
			h := uuidNameHeld(t, p, c, q)
			fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example))
			run(fixture.Exec("SET search_path TO public", 0))
			rows, err := h.Statement.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("offpath held cursor")
			}
			d := uuidOsspMissing(f.name+"(unknown, unknown)", 8)
			fixture.AssertDiagnostic(t, err, &d)
			h.Statement.Close()
		}
	})
}
