package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructIdentity(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			signature, source, result string
			strict                    bool
		}{
			{"hstore_in(cstring)", "hstore_in", "hstore", true},
			{"hstore_out(hstore)", "hstore_out", "cstring", true},
			{"hstore_to_array(hstore)", "hstore_to_array", "text[]", true},
			{"hstore_to_matrix(hstore)", "hstore_to_matrix", "text[]", true},
			{"hstore(text,text)", "hstore_from_text", "hstore", false},
			{"hstore(text[],text[])", "hstore_from_arrays", "hstore", false},
			{"hstore(text[])", "hstore_from_array", "hstore", true},
			{"fetchval(hstore,text)", "hstore_fetchval", "text", true},
			{"slice_array(hstore,text[])", "hstore_slice_to_array", "text[]", true},
			{"exist(hstore,text)", "hstore_exists", "boolean", true},
			{"defined(hstore,text)", "hstore_defined", "boolean", true},
		} {
			q := "SELECT p.prosrc,p.proisstrict,p.proretset,p.provolatile::text,p.proparallel::text,p.prorettype=$2::regtype AS result,l.lanname::text AS language FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure) p JOIN pg_language l ON l.oid=p.prolang"
			hstoreRun(t, p, b, c, fixture.Select(q, []string{"prosrc", "proisstrict", "proretset", "provolatile", "proparallel", "result", "language"}, []string{"TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT"}, [][]any{{tc.source, tc.strict, false, "i", "s", true, "c"}}, tc.signature, tc.result))
			hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.signature))
		}
		for _, tc := range []struct{ signature, procedure, result string }{
			{"%%(NONE,hstore)", "hstore_to_array(hstore)", "text[]"},
			{"%#(NONE,hstore)", "hstore_to_matrix(hstore)", "text[]"},
			{"->(hstore,text)", "fetchval(hstore,text)", "text"},
			{"->(hstore,text[])", "slice_array(hstore,text[])", "text[]"},
		} {
			hstoreRun(t, p, b, c, fixture.Select("SELECT oprcode=$2::regprocedure AS backing,oprresult=$3::regtype AS result FROM pg_operator WHERE oid=$1::regoperator", []string{"backing", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, tc.signature, tc.procedure, tc.result))
			hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.signature))
		}
	})
}

func TestReferenceHstoreConstructPresentation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_construct_namespace")
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_construct_namespace", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_presentation(k text,w text,v hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_construct_presentation VALUES ('a','1',hstore('a','1'))", 1))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_construct_presentation_view AS SELECT hstore(k,w)::text AS made,fetchval(v,k) AS fetched,v->k AS scalar,(v->ARRAY[k])::text AS requested FROM hs_construct_presentation", 0))
		q := "SELECT v->k AS scalar,(v->ARRAY[k])::text AS requested FROM hs_construct_presentation"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hstoreRun(t, p, b, held, fixture.Select(q, []string{"scalar", "requested"}, []string{"TEXT", "TEXT"}, [][]any{{"1", "{1}"}}))
		for _, tc := range []struct{ signature, name, renamed string }{
			{"text,text", "hstore", "hs_construct_pair"},
			{"hstore,text", "fetchval", "hs_construct_fetch"},
			{"hstore,text[]", "slice_array", "hs_construct_slice"},
		} {
			var before, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", tc.name+"("+tc.signature+")").Scan(&before); err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.name+"("+tc.signature+") RENAME TO "+tc.renamed, 0))
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.renamed+"("+tc.signature+") SET SCHEMA hs_construct_namespace", 0))
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "hs_construct_namespace."+tc.renamed+"("+tc.signature+")").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("presentation replaced procedure %d -> %d", before, after)
			}
		}
		hstoreRun(t, p, b, held, fixture.Select(q, []string{"scalar", "requested"}, []string{"TEXT", "TEXT"}, [][]any{{"1", "{1}"}}))
		hstoreRun(t, p, b, c, fixture.Select("SELECT made,fetched,scalar,requested FROM hs_construct_presentation_view", []string{"made", "fetched", "scalar", "requested"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{`"a"=>"1"`, "1", "1", "{1}"}}))
		hstoreRun(t, p, b, c, fixture.Select("SELECT hs_construct_namespace.hs_construct_pair(k,w)::text AS made,hs_construct_namespace.hs_construct_fetch(v,k) AS fetched,hs_construct_namespace.hs_construct_slice(v,ARRAY[k])::text AS requested FROM hs_construct_presentation", []string{"made", "fetched", "requested"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{`"a"=>"1"`, "1", "{1}"}}))
	})
}
