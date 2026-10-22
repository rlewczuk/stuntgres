package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func uuidOsspNamespaceInventory(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	seen := make(map[int64]bool)
	for _, tc := range uuidOsspNamespaces {
		var oid, ns, ext, nargs, result, owner int64
		var vol, parallel, signature string
		var strict bool
		q := "SELECT p.oid,p.pronamespace,e.oid,p.pronargs,p.prorettype,p.proowner,p.provolatile,p.proisstrict,p.proparallel,p.oid::regprocedure::text FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='uuid-ossp' AND p.proname=$1"
		if err := c.QueryRowContext(p.Ctx, q, tc.name).Scan(&oid, &ns, &ext, &nargs, &result, &owner, &vol, &strict, &parallel, &signature); err != nil {
			t.Fatal(tc.name, err)
		}
		if oid <= 0 || ns <= 0 || ext <= 0 || owner <= 0 || seen[oid] || nargs != 0 || result != 2950 || vol != "i" || !strict || parallel != "s" || signature != "uuid_ossp_part."+tc.name+"()" {
			t.Fatalf("invalid constant declaration %s: %d %d %d %d %d %d %s %v %s %s", tc.name, oid, ns, ext, nargs, result, owner, vol, strict, parallel, signature)
		}
		seen[oid] = true
		var n int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", oid, ext).Scan(&n); err != nil || n != 1 {
			t.Fatal(tc.name, "membership", n, err)
		}
	}
}

func TestReferenceUUIDOsspNamespaceIdentity(t *testing.T) {
	uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, tc := range uuidOsspNamespaces {
			uuidOsspFailure(t, p, c, "SELECT "+tc.name+"()", uuidOsspMissing(tc.name+"()", 8))
		}
		run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("ROLLBACK", 0))
		for _, tc := range uuidOsspNamespaces {
			uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part."+tc.name+"()", uuidOsspMissing("uuid_ossp_part."+tc.name+"()", 8))
		}
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		uuidOsspNamespaceInventory(t, p, c)
		for _, tc := range uuidOsspNamespaces {
			uuidOsspFailure(t, p, c, "SELECT "+tc.name+"()", uuidOsspMissing(tc.name+"()", 8))
			run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
			run(uuidOsspNamespaceSelect("SELECT "+tc.name+"() AS u", tc.value))
			run(fixture.Exec("SET search_path TO public", 0))
		}
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}))
		}
	})
}

func TestReferenceUUIDOsspNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "fresh_prepared"
		}
		t.Run(name, func(t *testing.T) {
			uuidOsspNamespaceCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				uuidOsspNamespaceInstall(t, p, c, r)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE USER uuid_ossp_owner", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
				actor := func(body func()) { fixture.RoleGrantActor(run, "uuid_ossp_owner", body) }
				for _, tc := range uuidOsspNamespaces {
					sig := "uuid_ossp_part." + tc.name + "()"
					q := "SELECT " + sig + " AS u"
					var held *sql.Stmt
					actor(func() {
						var err error
						held, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						run(uuidOsspNamespaceSelect(q, tc.value))
					})
					defer held.Close()
					h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
					run(fixture.Exec("CREATE TABLE uuid_ossp_part."+tc.name+"_saved(id integer,u uuid DEFAULT "+sig+")", 0))
					run(fixture.Exec("INSERT INTO uuid_ossp_part."+tc.name+"_saved(id) VALUES(1)", 1))
					run(fixture.Exec("CREATE VIEW uuid_ossp_part."+tc.name+"_view AS SELECT "+sig+" AS u", 0))
					run(fixture.Exec("GRANT SELECT ON uuid_ossp_part."+tc.name+"_view TO uuid_ossp_owner", 0))
					run(fixture.Exec("GRANT SELECT,INSERT ON uuid_ossp_part."+tc.name+"_saved TO uuid_ossp_owner", 0))
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC", 0))
					denied := func(query string) fixture.Step {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
						s.SelectRows = true
						s.ErrorStage = "execute"
						return s
					}
					for range 2 {
						actor(func() {
							run(denied(q))
							fixture.RunStep(t, p.Ctx, h, r, denied(q))
							run(denied(q + " WHERE FALSE"))
							run(denied("SELECT u FROM uuid_ossp_part." + tc.name + "_view"))
							run(denied("INSERT INTO uuid_ossp_part." + tc.name + "_saved(id,u) VALUES(2,'" + tc.value + "'),(3,DEFAULT) RETURNING u"))
							run(fixture.Select("SELECT id FROM uuid_ossp_part."+tc.name+"_saved ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
							run(fixture.Select("SELECT CASE WHEN FALSE THEN "+sig+" ELSE NULL END AS u", []string{"u"}, []string{"UUID"}, [][]any{{nil}}))
						})
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig+" TO uuid_ossp_owner", 0))
						actor(func() {
							run(uuidOsspNamespaceSelect(q, tc.value))
							fixture.RunStep(t, p.Ctx, h, r, uuidOsspNamespaceSelect(q, tc.value))
						})
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM uuid_ossp_owner", 0))
					}
				}
			})
		})
	}
}
