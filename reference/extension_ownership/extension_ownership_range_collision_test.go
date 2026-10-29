package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// A genuine subtype_diff can occupy the final range constructor signature:
// renaming the type does not rename the generated constructors. Replay must
// create the real baseline before publishing these independently named objects.
func ownershipRangeConstructorCollision(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		for _, q := range []string{`CREATE FUNCTION eo_old.current_range(integer,integer) RETURNS double precision LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT $1::double precision-$2::double precision'`, `CREATE TYPE eo_old.original AS RANGE(SUBTYPE=integer,SUBTYPE_DIFF=eo_old.current_range,MULTIRANGE_TYPE_NAME=eo_old.original_multi)`, `ALTER TYPE eo_old.original RENAME TO current_range`, `ALTER EXTENSION unaccent ADD TYPE eo_old.current_range`, `ALTER EXTENSION unaccent ADD FUNCTION eo_old.current_range(integer,integer)`, `ALTER EXTENSION unaccent SET SCHEMA eo_new`, `CREATE TABLE eo_other.range_values(v eo_new.current_range DEFAULT eo_old.original(1,3))`} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO eo_other.range_values DEFAULT VALUES`, 1))
		run(fixture.Exec(`INSERT INTO eo_other.range_values VALUES(NULL)`, 1))
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			ownershipMemberCount(r, 11)
			r(fixture.Select(`SELECT p.proname,n.nspname,count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname IN ('original','original_multi','current_range') GROUP BY p.proname,n.nspname ORDER BY p.proname`, []string{"proname", "nspname", "count"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"current_range", "eo_new", int64(1)}, {"original", "eo_old", int64(2)}, {"original_multi", "eo_old", int64(3)}}))
			r(ownershipCount(`SELECT count(*) FROM pg_range WHERE rngtypid='eo_new.current_range'::regtype AND rngmultitypid='eo_new.original_multi'::regtype AND rngsubdiff='eo_new.current_range(integer,integer)'::regprocedure`, 1))
			s := fixture.Select(`SELECT eo_new.current_range(1,3) AS difference,eo_old.original(1,3)::text AS value,eo_old.original_multi(eo_old.original(1,3))::text AS multivalue,pg_typeof(eo_old.original(1,3))::text AS actual_type`, []string{"difference", "value", "multivalue", "actual_type"}, []string{"FLOAT8", "TEXT", "TEXT", "TEXT"}, [][]any{{float64(-2), "[1,3)", "{[1,3)}", "eo_new.current_range"}})
			r(s)
			r(fixture.Prepared(s))
			s = fixture.Select(`SELECT v::text AS v FROM eo_other.range_values ORDER BY v::text NULLS LAST`, []string{"v"}, []string{"TEXT"}, [][]any{{"[1,3)"}, {nil}})
			r(s)
			r(fixture.Prepared(s))
			r(fixture.Exec(`BEGIN`, 0))
			r(fixture.Exec(`INSERT INTO eo_other.range_values DEFAULT VALUES`, 1))
			r(ownershipCount(`SELECT count(*) FROM eo_other.range_values WHERE v IS NOT NULL`, 2))
			r(fixture.Exec(`ROLLBACK`, 0))
			r(ownershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname NOT IN ('public','eo_old','eo_new','eo_other','information_schema') AND nspname NOT LIKE 'pg_%'`, 0))
		}
		check(c, b.Recorder)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		connector, err := stuntgres.NewDriver().OpenConnector("eo_range_collision")
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
}
