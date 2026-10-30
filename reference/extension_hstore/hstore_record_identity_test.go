package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreRecordIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, constructor, replacement string) {
	t.Helper()
	for _, tc := range []struct{ signature, source, result, args string }{
		{constructor + "(record)", "hstore_from_record", "hstore", "2249"},
		{replacement + "(anyelement,hstore)", "hstore_populate_record", "anyelement", "2283"},
	} {
		q := "SELECT p.prosrc,p.proisstrict,p.proretset,p.provolatile::text,p.proparallel::text,p.prorettype=$2::regtype AS result,l.lanname::text AS language,p.probin FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure) p JOIN pg_language l ON l.oid=p.prolang"
		hstoreRun(t, p, b, c, fixture.Select(q, []string{"prosrc", "proisstrict", "proretset", "provolatile", "proparallel", "result", "language", "probin"}, []string{"TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT"}, [][]any{{tc.source, false, false, "i", "s", true, "c", "$libdir/hstore"}}, tc.signature, tc.result))
		hstoreRun(t, p, b, c, fixture.Select("SELECT proargtypes[0]=$2::oid AS declared FROM pg_proc WHERE oid=$1::regprocedure", []string{"declared"}, []string{"BOOL"}, [][]any{{true}}, tc.signature, tc.args))
		hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.signature))
	}
	hstoreRun(t, p, b, c, fixture.Select("SELECT oprcode=$1::regprocedure AS backing,oprleft='anyelement'::regtype AS left_type,oprright='hstore'::regtype AS right_type,oprresult='anyelement'::regtype AS result,oprcom=0 AS no_commutator FROM pg_operator WHERE oid='#=(anyelement,hstore)'::regoperator", []string{"backing", "left_type", "right_type", "result", "no_commutator"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}}, replacement+"(anyelement,hstore)"))
	hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_operator'::regclass AND objid='#=(anyelement,hstore)'::regoperator AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func TestReferenceHstoreRecordIdentity(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		hstoreRecordIdentity(t, p, b, c, "hstore", "populate_record")
	})
}

func TestReferenceHstoreRecordSearchPath(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_record_shadow")
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_record_path(r hs_record_pair,h hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_path VALUES ('(7,keep)','i=>42')", 1))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_record_path_view AS SELECT hstore(r)::text AS made,populate_record(r,h)::text AS named,(r #= h)::text AS operated FROM hs_record_path", 0))
		for _, q := range []string{
			"CREATE SCHEMA hs_record_shadow",
			"CREATE FUNCTION hs_record_shadow.hstore(hs_record_pair) RETURNS text LANGUAGE SQL IMMUTABLE AS 'SELECT ''shadow''::text'",
			"CREATE FUNCTION hs_record_shadow.populate_record(hs_record_pair,hstore) RETURNS text LANGUAGE SQL IMMUTABLE AS 'SELECT ''shadow''::text'",
			"SET search_path=hs_record_shadow,public,pg_catalog",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT hstore(r) AS v FROM hs_record_path", "shadow"))
		hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT populate_record(r,h) AS v FROM hs_record_path", "shadow"))
		hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT public.hstore(r)::text AS v FROM hs_record_path", `"i"=>"7", "n"=>"keep"`))
		hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT public.populate_record(r,h)::text AS v FROM hs_record_path", "(42,keep)"))
		hstoreRecordBoth(t, p, b, c, fixture.Select("SELECT made,named,operated FROM public.hs_record_path_view", []string{"made", "named", "operated"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{`"i"=>"7", "n"=>"keep"`, "(42,keep)", "(42,keep)"}}))
		hstoreRun(t, p, b, c, fixture.Exec("RESET search_path", 0))
	})
}
