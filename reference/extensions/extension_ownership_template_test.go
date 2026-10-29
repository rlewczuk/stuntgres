package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This class fixture retains the real installed unaccent template and its full
// membership assertions; standalone lifecycle survivors are covered separately.
func ownershipInstalledTemplateClass(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_old`, 0))
		id := ownershipID(t, p, c, `SELECT oid::bigint FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='eo_old'::regnamespace`)
		helperEdges := `SELECT refobjid::bigint,deptype FROM pg_depend WHERE classid='pg_ts_template'::regclass AND objid=` + ownershipNumber(id) + ` AND refclassid='pg_proc'::regclass ORDER BY refobjid,deptype`
		before := ownershipSnapshot(t, p, c, helperEdges)
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP TEXT SEARCH TEMPLATE eo_old.unaccent`, 0))
		ownershipMemberCount(run, 5)
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH TEMPLATE eo_old.unaccent`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='uuid-ossp') AND deptype='e'`, 11))
		run(ownershipError(`ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH TEMPLATE eo_old.unaccent`, "55000", `text search template eo_old.unaccent is already a member of extension "uuid-ossp"`))
		run(ownershipError(`ALTER EXTENSION unaccent DROP TEXT SEARCH TEMPLATE eo_old.unaccent`, "55000", `text search template eo_old.unaccent is not a member of extension "unaccent"`))
		ownershipMemberCount(run, 5)
		ownershipUnchanged(t, p, c, helperEdges, before)
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" SET SCHEMA eo_new`, 0))
		run(fixture.Select(`SELECT oid::bigint FROM pg_ts_template WHERE tmplname='unaccent' AND tmplnamespace='eo_new'::regnamespace`, []string{"oid"}, []string{"INT8"}, [][]any{{id}}))
		ownershipUnchanged(t, p, c, helperEdges, before)
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			ownershipCallable(r, "eo_old")
			ownershipMemberCount(r, 5)
			r(ownershipCount(`SELECT count(*) FROM pg_ts_template t JOIN pg_depend d ON d.classid='pg_ts_template'::regclass AND d.objid=t.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE t.tmplname='unaccent' AND t.tmplnamespace='eo_new'::regnamespace AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='uuid-ossp'`, 1))
			r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='uuid-ossp') AND deptype='e'`, 11))
			r(ownershipCount(`SELECT count(*) FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate JOIN pg_depend dep ON dep.classid='pg_ts_dict'::regclass AND dep.objid=d.oid AND dep.refclassid='pg_ts_template'::regclass AND dep.refobjid=t.oid AND dep.deptype='n' WHERE d.dictname='unaccent' AND d.dictnamespace='eo_old'::regnamespace AND t.tmplname='unaccent' AND t.tmplnamespace='eo_new'::regnamespace`, 1))
			r(ownershipCount(`SELECT count(*) FROM pg_ts_template t JOIN pg_proc init ON init.oid=t.tmplinit JOIN pg_proc lexize ON lexize.oid=t.tmpllexize WHERE t.tmplname='unaccent' AND t.tmplnamespace='eo_new'::regnamespace AND init.proname='unaccent_init' AND lexize.proname='unaccent_lexize' AND init.pronamespace='eo_old'::regnamespace AND lexize.pronamespace='eo_old'::regnamespace`, 1))
			r(fixture.Select(`SELECT ts_lexize('eo_old.unaccent','Hôtel') AS translated`, []string{"translated"}, []string{"_TEXT"}, [][]any{{"{Hotel}"}}))
		}
		check(c, b.Recorder)
		if !b.PG {
			saved := extensionDump(t, p.Ctx, p.Native)
			connector, err := stuntgres.NewDriver().OpenConnector("eo_template_remap")
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
		}
		run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP TEXT SEARCH TEMPLATE eo_new.unaccent`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD TEXT SEARCH TEMPLATE eo_new.unaccent`, 0))
		ownershipMemberCount(run, 6)
		ownershipCallable(run, "eo_old")
	})
}
