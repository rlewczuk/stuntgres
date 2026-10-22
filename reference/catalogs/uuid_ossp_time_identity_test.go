package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspTimeIdentity(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, name := range uuidOsspTimeNames {
			uuidOsspFailure(t, p, c, "SELECT "+name+"()", uuidOsspMissing(name+"()", 8))
		}
		run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		var extension, namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid,extnamespace FROM pg_extension WHERE extname='uuid-ossp'").Scan(&extension, &namespace); err != nil || extension <= 0 || namespace <= 0 {
			t.Fatal(extension, namespace, err)
		}
		seen := map[int64]bool{}
		for _, name := range uuidOsspTimeNames {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT pronargs,prorettype::bigint AS prorettype,provolatile,proisstrict,proparallel FROM pg_proc WHERE pronamespace="+fixture.Position(int(namespace))+" AND proname='"+name+"'", []string{"pronargs", "prorettype", "provolatile", "proisstrict", "proparallel"}, []string{"INT2", "INT8", "CHAR", "BOOL", "CHAR"}, [][]any{{int64(0), int64(2950), "v", true, "s"}})
				step.Prepared = prepared
				run(step)
			}
			var oid, count int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_proc WHERE pronamespace=$1 AND proname=$2", namespace, name).Scan(&oid); err != nil || oid <= 0 || seen[oid] {
				t.Fatal("member identity", oid, err)
			}
			seen[oid] = true
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", oid, extension).Scan(&count); err != nil || count != 1 {
				t.Fatal("membership", count, err)
			}
			var ownerMatches bool
			if err := c.QueryRowContext(p.Ctx, "SELECT p.proowner=e.extowner FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='uuid-ossp' AND p.oid=$1", oid).Scan(&ownerMatches); err != nil || !ownerMatches {
				t.Fatal("installed member owner", ownerMatches, err)
			}
			uuidOsspFailure(t, p, c, "SELECT "+name+"()", uuidOsspMissing(name+"()", 8))
			uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part.\""+strings.ToUpper(name)+"\"()", uuidOsspMissing("uuid_ossp_part."+strings.ToUpper(name)+"()", 8))
			q := "SELECT uuid_ossp_part." + name + "() AS u"
			uuidOsspTimeRead(t, p, c, r, pg, q, 1, 1, name == "uuid_generate_v1mc")
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			uuidOsspTimeRead(t, p, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, pg, q, 1, 1, name == "uuid_generate_v1mc")
			s.Close()
		}
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace="+fixture.Position(int(namespace))+" AND proname IN ('uuid_generate_v3','uuid_generate_v5') AND pronargs=2 AND prorettype=2950 AND provolatile='i' AND proisstrict", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
		for _, name := range uuidOsspTimeNames {
			q := "SELECT " + name + "() AS u"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			uuidOsspTimeRead(t, p, held, r, pg, q, 1, 1, name == "uuid_generate_v1mc")
			run(fixture.Exec("SET search_path TO public", 0))
			rows, err := s.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("off-path held statement returned rows")
			}
			d := uuidOsspMissing(name+"()", 8)
			fixture.AssertDiagnostic(t, err, &d)
			uuidOsspFailure(t, p, c, q, d)
			run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
			uuidOsspTimeRead(t, p, held, r, pg, q, 1, 1, name == "uuid_generate_v1mc")
		}
	})
}
