package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func earthIdentityChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
	// Dynamic identities are relational, not allocator goldens. Domain IO uses
	// ordinary domain_in/domain_recv and the installed cube's actual output.
	// Keep each catalog relationship in a two-source witness. Filtering the
	// installed identities in WHERE avoids a cast-ended chained JOIN condition.
	run(fixture.Select("SELECT e.oid<>c.oid AND e.oid<>0 AS distinct_id,e.typtype,e.typbasetype=c.oid AS base,e.typinput::oid=2597 AS input,e.typreceive::oid=2598 AS receive,e.typoutput=c.typoutput AS output,e.typsend=c.typsend AS send FROM pg_type e JOIN pg_type c ON e.typbasetype=c.oid WHERE e.oid='earth'::regtype::oid AND c.oid='cube'::regtype::oid", []string{"distinct_id", "typtype", "base", "input", "receive", "output", "send"}, []string{"BOOL", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, "d", true, true, true, true, true}}))
	run(fixture.Select("SELECT e.typarray=a.oid AND a.typelem=e.oid AS companion FROM pg_type e JOIN pg_type a ON a.oid=e.typarray WHERE e.oid='earth'::regtype::oid AND a.oid='earth[]'::regtype::oid", []string{"companion"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT conname,contype,convalidated,contypid='earth'::regtype AS actual,oid<>0 AS identity FROM pg_constraint WHERE contypid='earth'::regtype ORDER BY conname", []string{"conname", "contype", "convalidated", "actual", "identity"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"not_3d", "c", true, true, true}, {"not_point", "c", true, true, true}, {"on_surface", "c", true, true, true}}))
	run(fixture.Select("SELECT t.typname FROM pg_depend d JOIN pg_type t ON t.oid=d.objid WHERE d.classid='pg_type'::regclass::oid AND d.refclassid='pg_extension'::regclass::oid AND d.deptype='e' AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='earthdistance') ORDER BY t.typname", []string{"typname"}, []string{"NAME"}, [][]any{{"_earth"}, {"earth"}}))
	if !b.PG {
		run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('earth','cube_dim','cube_is_point','cube_distance','ll_to_earth','earth_distance') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typsend::oid=0 AS text_only FROM pg_type WHERE oid='cube'::regtype", []string{"text_only"}, []string{"BOOL"}, [][]any{{true}}))
	}
}

func TestReferenceEarthMetadata(t *testing.T) {
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE earth_metadata(id integer NOT NULL,v earth,n earth NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO earth_metadata VALUES (1,'(6378168,0)','6378168'),(2,NULL,'(6378168,0,0)')", 2))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT id,v AS alias,n FROM earth_metadata ORDER BY id", [][]any{{int64(1), "(6378168, 0)", "(6378168)"}, {int64(2), nil, "(6378168, 0, 0)"}}},
			{"SELECT id,v AS alias,n FROM earth_metadata WHERE id=2", [][]any{{int64(2), nil, "(6378168, 0, 0)"}}},
			{"SELECT id,v AS alias,n FROM earth_metadata WHERE false", [][]any{}},
		} {
			s := earthSelect(b, tc.q, []string{"id", "alias", "n"}, tc.rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true, false}) }
			run(s)
			run(fixture.Prepared(s))
			held := earthHeld(t, p, c, tc.q)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			held.Statement.Close()
		}
		earthIdentityChecks(t, p, b, c, b.Recorder)
	})
}
