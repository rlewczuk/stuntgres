package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONMetadata(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_json_source(id integer,v hstore)", 0))
		for _, name := range []string{"hstore_to_json", "hstore_to_json_loose"} {
			identity := hstoreLiteral(name+"(hstore)") + "::regprocedure::oid"
			hstoreRun(t, p, b, c, fixture.Select("SELECT proargtypes[0]='hstore'::regtype AS formal,prorettype=114 AS result FROM pg_proc WHERE oid="+identity, []string{"formal", "result"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			hstoreRun(t, p, b, c, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1255 AND objid="+identity+" AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')", []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
			hstoreRun(t, p, b, c, fixture.Select("SELECT refobjid='hstore'::regtype AS formal,deptype FROM pg_depend WHERE classid=1255 AND objid="+identity+" AND refclassid=1247", []string{"formal", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
		}
		hstoreRun(t, p, b, c, fixture.Select("SELECT p.proname,p.pronargs,p.prorettype::integer AS result,p.proisstrict,p.provolatile,p.proparallel,p.proretset,l.lanname,p.prosrc FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.pronamespace='public'::regnamespace AND p.proname IN ('hstore_to_json','hstore_to_json_loose') ORDER BY p.proname", []string{"proname", "pronargs", "result", "proisstrict", "provolatile", "proparallel", "proretset", "lanname", "prosrc"}, []string{"NAME", "INT2", "INT4", "BOOL", "CHAR", "CHAR", "BOOL", "NAME", "TEXT"}, [][]any{{"hstore_to_json", int64(1), int64(114), true, "i", "s", false, "c", "hstore_to_json"}, {"hstore_to_json_loose", int64(1), int64(114), true, "i", "s", false, "c", "hstore_to_json_loose"}}))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_json_source VALUES (1,'a=>1.00'),(2,''),(3,NULL),(4,'a=>NULL'),(5,'a=>\"NULL\"')", 5))
		want := [][]any{{`{"a": "1.00"}`, `{"a": 1.00}`}, {`{}`, `{}`}, {nil, nil}, {`{"a": null}`, `{"a": null}`}, {`{"a": "NULL"}`, `{"a": "NULL"}`}}
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{" ORDER BY id", want}, {" WHERE id=0", [][]any{}}, {" WHERE id=3", [][]any{{nil, nil}}},
		} {
			q := hstoreJSONQuery("v") + " FROM hs_json_source" + tc.suffix
			s := hstoreJSONPair(b, q, tc.rows)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, s)
			stmt.Close()
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_json_result(id integer,strict_json json,loose_json json)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_json_result VALUES (1,hstore_to_json('a=>1.00'),hstore_to_json_loose('a=>1.00'))", 1))
		insert := "INSERT INTO hs_json_result VALUES ($1,hstore_to_json($2::hstore),hstore_to_json_loose($2::hstore))"
		for i, value := range []any{"", nil, "a=>NULL", `a=>"NULL"`} {
			hstoreRun(t, p, b, c, fixture.Prepared(fixture.Exec(insert, 1, i+2, value)))
		}
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{" ORDER BY id", want}, {" WHERE id=0", [][]any{}}, {" WHERE id=3", [][]any{{nil, nil}}},
		} {
			q := "SELECT strict_json,loose_json FROM hs_json_result" + tc.suffix
			s := hstoreJSONSelect(b, q, []string{"strict_json", "loose_json"}, tc.rows, true)
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}
