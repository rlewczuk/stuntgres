package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ownershipRangePlacement(t *testing.T, controls bool) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		for _, q := range []string{`CREATE TYPE eo_old.span AS RANGE (SUBTYPE=integer,MULTIRANGE_TYPE_NAME=eo_old.spans)`, `ALTER EXTENSION unaccent ADD TYPE eo_old.span`, `ALTER TYPE eo_old.spans SET SCHEMA eo_other`} {
			run(fixture.Exec(q, 0))
		}
		q := `SELECT t.oid::bigint,t.typname,n.nspname,t.typarray::bigint FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname IN ('eo_old','eo_new','eo_other') ORDER BY t.oid`
		before := ownershipSnapshot(t, p, c, q)
		s := ownershipError(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, "0A000", `extension "unaccent" does not support SET SCHEMA`)
		s.Diagnostic.Detail = `type eo_other.spans is not in the extension's schema "eo_old"`
		run(s)
		ownershipUnchanged(t, p, c, q, before)
		ownershipMemberCount(run, 10)
		run(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_old'::regnamespace`, 1))
		for _, q := range []string{`ALTER TYPE eo_other.spans SET SCHEMA eo_old`, `ALTER EXTENSION unaccent SET SCHEMA eo_new`} {
			run(fixture.Exec(q, 0))
		}
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			r(fixture.Select(`SELECT t.typname,n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE t.typname IN ('span','spans','_span','_spans') ORDER BY t.typname`, []string{"typname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"_span", "eo_new"}, {"_spans", "eo_new"}, {"span", "eo_new"}, {"spans", "eo_new"}}))
			r(fixture.Select(`SELECT p.proname,n.nspname,count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname IN ('span','spans') GROUP BY p.proname,n.nspname ORDER BY p.proname`, []string{"proname", "nspname", "count"}, []string{"NAME", "NAME", "INT8"}, [][]any{{"span", "eo_old", int64(2)}, {"spans", "eo_old", int64(3)}}))
			r(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND p.proname IN ('span','spans')`, 0))
			s := fixture.Select(`SELECT eo_old.span(1,3)::text AS value,eo_old.spans(eo_old.span(1,3))::text AS multivalue,pg_typeof(eo_old.span(1,3))::text AS actual_type`, []string{"value", "multivalue", "actual_type"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"[1,3)", "{[1,3)}", "eo_new.span"}})
			r(s)
			r(fixture.Prepared(s))
			ownershipCallable(r, "eo_new")
		}
		check(c, b.Recorder)
		if !controls || b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		connector, err := stuntgres.NewDriver().OpenConnector("eo_range_placement")
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
