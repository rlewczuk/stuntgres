package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// These released float8-only functions do not acquire an incidental dependency
// on the Earth/cube nominal types. The real sec/gc -> earth() dependency remains.
func ownershipEarthScalarSurvivors(t *testing.T) {
	fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := fixture.OwnershipRun(t, p, b, c)
		for _, q := range []string{`CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`, `CREATE EXTENSION cube SCHEMA eo_new`, `CREATE EXTENSION earthdistance SCHEMA eo_old`} {
			run(fixture.Exec(q, 0))
		}
		for _, target := range []string{`FUNCTION eo_old.earth()`, `FUNCTION eo_old.sec_to_gc(double precision)`, `FUNCTION eo_old.gc_to_sec(double precision)`} {
			run(fixture.Exec("ALTER EXTENSION earthdistance DROP "+target, 0))
		}
		identityQuery := `SELECT p.oid::bigint,p.proname,p.proowner::bigint,p.proacl::text,p.prosrc FROM pg_proc p WHERE p.pronamespace='eo_old'::regnamespace AND p.proname IN ('earth','sec_to_gc','gc_to_sec') ORDER BY p.proname`
		identity := fixture.OwnershipSnapshot(t, p, c, identityQuery)
		if len(identity) != 3 {
			t.Fatal("genuine scalar cohort must contain radius and both conversions", identity)
		}
		checkDependencies := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			for _, name := range []string{"sec_to_gc", "gc_to_sec"} {
				step := fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_proc'::regclass AND refobjid='eo_old.earth()'::regprocedure AND deptype='n'`, 1, "eo_old."+name+"(double precision)")
				r(step)
			}
		}
		checkDependencies(t, c, b.Recorder)
		run(fixture.Exec(`DROP EXTENSION earthdistance`, 0))
		run(fixture.Exec(`DROP EXTENSION cube`, 0))
		fixture.OwnershipUnchanged(t, p, c, identityQuery, identity)
		check := func(t *testing.T, conn *sql.Conn, recorder *fixture.Recorder) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname IN ('cube','earthdistance')`, 0))
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_type WHERE typnamespace IN ('eo_old'::regnamespace,'eo_new'::regnamespace) AND typname IN ('earth','_earth','cube','_cube')`, 0))
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proname IN ('earth','sec_to_gc','gc_to_sec')`, 3))
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid IN ('eo_old.earth()'::regprocedure,'eo_old.sec_to_gc(double precision)'::regprocedure,'eo_old.gc_to_sec(double precision)'::regprocedure) AND deptype='e'`, 0))
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid IN ('eo_old.sec_to_gc(double precision)'::regprocedure,'eo_old.gc_to_sec(double precision)'::regprocedure) AND refclassid='pg_proc'::regclass AND refobjid='eo_old.earth()'::regprocedure AND deptype='n'`, 2))
			checkDependencies(t, conn, recorder)
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname LIKE '__stuntgres_install%' OR nspname LIKE '__stuntgres_prerequisite%'`, 0))
			s := fixture.Select(`SELECT eo_old.earth() AS radius,eo_old.sec_to_gc(0::double precision) AS arc,eo_old.gc_to_sec(0::double precision) AS chord,eo_old.sec_to_gc(NULL::double precision) AS missing`, []string{"radius", "arc", "chord", "missing"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0), float64(0), nil}})
			r(s)
			r(fixture.Prepared(s))
			// Lawful CASCADE exercises the genuine parsed SQL-body dependency,
			// without inventing an English ordering for multiple detail lines.
			r(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			r(fixture.Exec(`DROP FUNCTION eo_old.earth() CASCADE`, 0))
			r(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='eo_old'::regnamespace AND proname IN ('earth','sec_to_gc','gc_to_sec')`, 0))
			r(fixture.Exec("ROLLBACK", 0))
		}
		check(t, c, b.Recorder)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		install := strings.Index(saved, `CREATE EXTENSION "earthdistance"`)
		detach := strings.Index(saved, `DROP FUNCTION "__stuntgres_install_0"."earth"`)
		retireEarth := strings.Index(saved, `DROP EXTENSION "earthdistance";`)
		retireCube := strings.Index(saved, `DROP EXTENSION "cube";`)
		if install < 0 || detach <= install || retireEarth <= detach || retireCube <= retireEarth ||
			!strings.Contains(saved, `CREATE EXTENSION "cube" WITH SCHEMA "__stuntgres_prerequisite_cube"`) {
			t.Fatal("scalar replay must genuinely install and retire its temporary prerequisite cohort", saved)
		}
		connector, err := stuntgres.NewDriver().OpenConnector("eo_earth_scalar_survivors")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		independent := sql.OpenDB(connector)
		defer independent.Close()
		for _, destination := range []struct {
			name     string
			control  *sql.DB
			recorded *sql.DB
			recorder *fixture.Recorder
		}{
			{"same_driver", p.Native, b.DB, b.Recorder},
			{"independent_driver", independent, recorded, recorder},
		} {
			t.Run(destination.name, func(t *testing.T) {
				if err := stuntgres.Reset(p.Ctx, destination.control); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, destination.control, strings.NewReader(saved)); err != nil {
					t.Fatal(err)
				}
				conn, err := destination.recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				// The preceding CASCADE records zero for the same procedure-count
				// query. This destination must record its own restored count of three,
				// not reuse that earlier result through an unwrapped control pool.
				check(t, conn, destination.recorder)
			})
		}
	})
}
