package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLPredicateLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "xml_predicate_schema", "xml_predicate_schema_renamed")
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE xml_predicate_data(id int,x xml)", 0),
			fixture.Exec("INSERT INTO xml_predicate_data VALUES(1,XMLPARSE(CONTENT '<r><a>1</a></r>')),(2,XMLPARSE(CONTENT '<a/><b/>')),(3,NULL)", 3),
			fixture.Exec("CREATE VIEW xml_predicate_view AS SELECT id,x IS DOCUMENT AS d,x IS NOT DOCUMENT AS n,xml_is_well_formed_document(x::text) AS w,CASE WHEN x IS DOCUMENT THEN XMLEXISTS('/r/a' PASSING BY VALUE x BY REF) ELSE NULL END AS e FROM xml_predicate_data", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,d,n,w,e FROM xml_predicate_view ORDER BY id", []string{"id", "d", "n", "w", "e"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, false, true, true}, {int64(2), false, true, false, nil}, {int64(3), nil, nil, nil, nil}})
		xmlBoth(t, p, b, c, want)
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('xml_predicate_view'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_predicate_replay AS "+definition, 0))
		replay := want
		replay.Query = "SELECT id,d,n,w,e FROM xml_predicate_replay ORDER BY id"
		xmlBoth(t, p, b, c, replay)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE xml_predicate_data RENAME TO xml_predicate_renamed", 0))
		xmlBoth(t, p, b, c, want)
		for _, q := range []string{"CREATE SCHEMA xml_predicate_schema", "ALTER TABLE xml_predicate_renamed SET SCHEMA xml_predicate_schema", "ALTER SCHEMA xml_predicate_schema RENAME TO xml_predicate_schema_renamed"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		xmlBoth(t, p, b, c, want)
		xmlBoth(t, p, b, c, replay)
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('xml_predicate_view'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_predicate_qualified_replay AS "+definition, 0))
		qualified := want
		qualified.Query = "SELECT id,d,n,w,e FROM xml_predicate_qualified_replay ORDER BY id"
		xmlBoth(t, p, b, c, qualified)
		held, err := c.PrepareContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		count := 0
		for old.Next() {
			var id int
			var d, n, w, e sql.NullBool
			if err := old.Scan(&id, &d, &n, &w, &e); err != nil {
				t.Fatal(err)
			}
			expected := want.Rows[count]
			if id != int(expected[0].(int64)) {
				t.Fatal(id)
			}
			for i, v := range []sql.NullBool{d, n, w, e} {
				if expected[i+1] == nil {
					if v.Valid {
						t.Fatal(v)
					}
				} else if !v.Valid || v.Bool != expected[i+1].(bool) {
					t.Fatal(v)
				}
			}
			count++
		}
		if err := old.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatal(count)
		}
		xmlBoth(t, p, b, c, want)
		xmlBoth(t, p, b, c, replay)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: want.Query}, b.Recorder, want)
		xmlBoth(t, p, b, c, qualified)
	})
	t.Run("assignment-source", func(t *testing.T) {
		xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION xml_predicate_source(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(DOCUMENT ''<r/>'')'", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE CAST (integer AS xml) WITH FUNCTION xml_predicate_source(integer)", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP CAST IF EXISTS (integer AS xml)"); err != nil {
					t.Error(err)
				}
			}()
			xmlFailure(t, p, b, c, "SELECT NULL::integer IS DOCUMENT", xmlError("42804", "argument of IS DOCUMENT must be type xml, not type integer", "", 8), true)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP CAST (integer AS xml)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE CAST (integer AS xml) WITH FUNCTION xml_predicate_source(integer) AS ASSIGNMENT", 0))
			xmlBoth(t, p, b, c, xmlSelect("SELECT NULL::integer IS DOCUMENT AS v", "BOOL", true))
			for _, q := range []string{"SELECT XMLEXISTS('/r' PASSING (NULL::integer)) AS v", "SELECT pg_catalog.xmlexists('/r',NULL::integer) AS v"} {
				diagnostic := xmlError("42883", "function pg_catalog.xmlexists(unknown, integer) does not exist", "", 8)
				diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				xmlFailure(t, p, b, c, q, diagnostic, true)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP CAST (integer AS xml)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE CAST (integer AS xml) WITH FUNCTION xml_predicate_source(integer) AS IMPLICIT", 0))
			for _, q := range []string{"SELECT NULL::integer IS DOCUMENT AS v", "SELECT XMLEXISTS('/r' PASSING (NULL::integer)) AS v", "SELECT pg_catalog.xmlexists('/r',NULL::integer) AS v"} {
				xmlBoth(t, p, b, c, xmlSelect(q, "BOOL", true))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_predicate_cast_view AS SELECT NULL::integer IS DOCUMENT AS v", 0))
			xmlBoth(t, p, b, c, xmlSelect("SELECT v FROM xml_predicate_cast_view", "BOOL", true))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_predicate_exists_cast_view AS SELECT XMLEXISTS('/r' PASSING (NULL::integer)) AS v", 0))
			existsQuery := "SELECT v FROM xml_predicate_exists_cast_view"
			existsWant := xmlSelect(existsQuery, "BOOL", true)
			xmlBoth(t, p, b, c, existsWant)
			existsHeld, err := c.PrepareContext(p.Ctx, existsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer existsHeld.Close()
			q := "SELECT v FROM xml_predicate_cast_view"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION xml_predicate_source(integer) RENAME TO xml_predicate_source_renamed", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, xmlSelect(q, "BOOL", true))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: existsHeld, SQL: existsQuery}, b.Recorder, existsWant)
			xmlBoth(t, p, b, c, existsWant)
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('xml_predicate_exists_cast_view'::regclass,true)").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW xml_predicate_exists_cast_replay AS "+definition, 0))
			replayWant := xmlSelect("SELECT v FROM xml_predicate_exists_cast_replay", "BOOL", true)
			xmlBoth(t, p, b, c, replayWant)
			if b.PG {
				return
			}
			old, err := c.QueryContext(p.Ctx, existsQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if !old.Next() {
				t.Fatal("cast-bearing retained reader lost its row", old.Err())
			}
			var value bool
			if err := old.Scan(&value); err != nil || !value {
				t.Fatal("cast-bearing retained reader changed", value, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("cast-bearing retained reader changed cardinality", old.Err())
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: existsHeld, SQL: existsQuery}, b.Recorder, existsWant)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, xmlSelect(q, "BOOL", true))
			xmlBoth(t, p, b, c, existsWant)
			xmlBoth(t, p, b, c, replayWant)
			xmlBoth(t, p, b, c, xmlSelect(q, "BOOL", true))
			fresh, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			xmlBoth(t, p, b, fresh, existsWant)
			xmlBoth(t, p, b, fresh, replayWant)
		})
	})
}
func TestReferenceXMLPredicateSmoke(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			want any
		}{
			{"SELECT XMLPARSE(CONTENT '<r/>') IS DOCUMENT AS v", true}, {"SELECT XMLPARSE(CONTENT '<a/><b/>') IS DOCUMENT AS v", false}, {"SELECT NULL::xml IS NOT DOCUMENT AS v", nil},
			{xmlExistsQuery(`/r/a[2][@id='2']`, xmlPredicateR), true}, {xmlExistsQuery(`/r/missing`, xmlPredicateR), false}, {xmlExistsQuery(`false()`, xmlPredicateR), true},
			{xmlExistsQuery(`/r/a[3]/preceding-sibling::*[1][@id='2']`, `<r><a id="1"/><a id="2"/><a id="3"/></r>`), true}, {xmlExistsQuery(`(/r/a[3]/preceding-sibling::*)[1][@id='1']`, `<r><a id="1"/><a id="2"/><a id="3"/></r>`), true},
			{xmlExistsQuery(`/r[string-length('é𝄞')=2 and substring('12345',2,3)='234']`, xmlPredicateR), true},
			{"SELECT XMLEXISTS('/r' PASSING BY VALUE ('<r/>'::xml) BY REF) AS v", true},
		} {
			xmlBoth(t, p, b, c, xmlSelect(tc.q, "BOOL", tc.want))
		}
		q := "SELECT xml_is_well_formed($1) AS v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, mode := range []string{"CONTENT", "DOCUMENT", "CONTENT"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET XML OPTION "+mode, 0))
			s := xmlSelect(q, "BOOL", mode == "CONTENT")
			s.Args = []any{"<a/><b/>"}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
		}
		xmlFailure(t, p, b, c, xmlExistsQuery("[", "<r/>"), xmlError("10608", "invalid XPath expression", "Invalid expression", 0), false)
		xmlFailure(t, p, b, c, xmlExistsQuery("false()", "<a/><b/>"), xmlError("2200M", "could not parse XML document", "line 1: Extra content at the end of the document\n<a/><b/>\n    ^", 0), false)
		exists := "SELECT XMLEXISTS($1 PASSING $2) AS v"
		held, err := c.PrepareContext(p.Ctx, exists)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		runner := fixture.HeldStatement{Executor: c, Statement: held, SQL: exists}
		failure := fixture.ErrorDiagnostic(exists, xmlError("10608", "invalid XPath expression", "Invalid expression", 0), "[", "<r/>")
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, failure)
		success := xmlSelect(exists, "BOOL", true)
		success.Args = []any{"/r", "<r/>"}
		fixture.RunStep(t, p.Ctx, runner, b.Recorder, success)
	})
}
