package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ownershipTablefuncSurvivors(t *testing.T, controls bool) {
	for _, foreign := range []bool{false, true} {
		name := "detached"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := ownershipRun(t, p, b, c)
				ownershipSetup(run)
				run(fixture.Exec(`CREATE EXTENSION tablefunc SCHEMA eo_old`, 0))
				id := ownershipID(t, p, c, `SELECT 'eo_old.normal_rand(integer,double precision,double precision)'::regprocedure::bigint`)
				metadataQuery := `SELECT p.oid::bigint,r.rolname,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=` + ownershipNumber(id)
				before := ownershipSnapshot(t, p, c, metadataQuery)
				run(fixture.Exec(`ALTER EXTENSION tablefunc DROP FUNCTION eo_old.normal_rand(integer,double precision,double precision)`, 0))
				run(fixture.Exec(`ALTER FUNCTION eo_old.normal_rand(integer,double precision,double precision) RENAME TO eo_draw`, 0))
				values := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					s := normalSelect(`SELECT eo_old.eo_draw(3,9,0) AS normal_rand`, 3, 9)
					r(s)
					r(fixture.Prepared(s))
					s = fixture.Select(`SELECT count(*) FROM eo_old.eo_draw(NULL::integer,0,1)`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
					r(s)
					r(fixture.Prepared(s))
				}
				values(c, b.Recorder)
				run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION eo_old.eo_draw(integer,double precision,double precision)`, 0))
				values(c, b.Recorder)
				run(fixture.Exec(`DROP EXTENSION tablefunc`, 0))
				values(c, b.Recorder)
				if !foreign {
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION eo_old.eo_draw(integer,double precision,double precision)`, 0))
				}
				ownershipUnchanged(t, p, c, metadataQuery, before)
				run(fixture.Exec(`CREATE VIEW eo_old.eo_draws AS SELECT eo_old.eo_draw(3,9,0) AS v`, 0))
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := ownershipRunWithRecorder(t, p, conn, recorder)
					values(conn, recorder)
					r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='tablefunc'`, 0))
					member := ""
					if foreign {
						member = "uuid-ossp"
					}
					r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.prosrc FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='eo_old.eo_draw(integer,double precision,double precision)'::regprocedure`, []string{"member_of", "proisstrict", "provolatile", "proretset", "proparallel", "prosrc"}, []string{"TEXT", "BOOL", "CHAR", "BOOL", "CHAR", "TEXT"}, [][]any{{member, true, "v", true, "u", "normal_rand"}}))
					s := fixture.Select(`SELECT v FROM eo_old.eo_draws`, []string{"v"}, []string{"FLOAT8"}, [][]any{{float64(9)}, {float64(9)}, {float64(9)}})
					r(s)
					r(fixture.Prepared(s))
				}
				check(c, b.Recorder)
				if !controls || b.PG {
					return
				}
				saved := extensionDump(t, p.Ctx, p.Native)
				connector, err := stuntgres.NewDriver().OpenConnector("eo_tablefunc_" + name)
				if err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				other := sql.OpenDB(connector)
				defer other.Close()
				for _, destination := range []struct {
					control, recorded *sql.DB
					recorder          *fixture.Recorder
				}{
					{p.Native, b.DB, b.Recorder},
					{other, recorded, recorder},
				} {
					db := destination.control
					if err := stuntgres.Reset(p.Ctx, db); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(saved)); err != nil {
						t.Fatal(err)
					}
					conn, err := destination.recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(conn, destination.recorder)
					conn.Close()
				}
			})
		})
	}
}
