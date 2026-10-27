package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordConstructor(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, tc := range []struct{ expression, value string }{
			{"'(7,keep)'::hs_record_pair", `"i"=>"7", "n"=>"keep"`},
			{"NULL::hs_record_pair", `"i"=>NULL, "n"=>NULL`},
			{"'(,)'::hs_record_pair", `"i"=>NULL, "n"=>NULL`},
			{"ROW(1,2)", `"f1"=>"1", "f2"=>"2"`},
			{"ROW(true,false)", `"f1"=>"t", "f2"=>"f"`},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore("+tc.expression+") AS v", []string{"v"}, [][]any{{tc.value}}, nil))
			})
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TYPE hs_record_empty AS ()", 0))
		for _, expression := range []string{"NULL::hs_record_empty", "'()'::hs_record_empty"} {
			hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore("+expression+") AS v", []string{"v"}, [][]any{{""}}, nil))
		}
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE TYPE hs_record_names AS ("N" text,"雪" text,"a""b" text,"c\d" text)`, 0))
		hstoreRecordBoth(t, p, b, c, hstoreSelect(b, `SELECT hstore(ROW('upper','多字','quote','slash')::hs_record_names) AS v`, []string{"v"}, [][]any{{`"N"=>"upper", "a\"b"=>"quote", "c\\d"=>"slash", "雪"=>"多字"`}}, nil))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_record_dropped(i integer,obsolete text,n text)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_dropped VALUES (7,'gone','keep')", 1))
		hstoreRun(t, p, b, c, fixture.Exec("ALTER TABLE hs_record_dropped DROP COLUMN obsolete", 0))
		hstoreRecordBoth(t, p, b, c, hstoreSelect(b, "SELECT hstore(r) AS v FROM hs_record_dropped r", []string{"v"}, [][]any{{`"i"=>"7", "n"=>"keep"`}}, nil))
	})
}

func TestReferenceHstoreRecordReplacement(t *testing.T) {
	for _, operator := range []bool{false, true} {
		label := "function"
		if operator {
			label = "operator"
		}
		t.Run(label, func(t *testing.T) {
			hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreRecordSetup(t, p, b, c)
				for _, tc := range []struct {
					base, source string
					value        any
				}{
					{"'(7,keep)'", "'i=>0042'", "(42,keep)"},
					{"'(7,keep)'", "'n=>changed'", "(7,changed)"},
					{"'(7,keep)'", "'unknown=>oops'", "(7,keep)"},
					{"'(7,keep)'", "'N=>changed'", "(7,keep)"},
					{"'(7,keep)'", "'n=>NULL'", "(7,)"},
					{"'(7,keep)'", `'n=>"NULL"'`, "(7,NULL)"},
					{"'(7,keep)'", `'n=>""'`, `(7,"")`},
					{"'(7,keep)'", "''", "(7,keep)"},
					{"'(7,keep)'", "NULL", "(7,keep)"},
					{"NULL", "'i=>42'", "(42,)"},
					{"NULL", "''", "(,)"},
					{"NULL", "NULL", nil},
					{"'(,)'", "NULL", "(,)"},
				} {
					q := "SELECT " + hstoreRecordCall(operator, tc.base+"::hs_record_pair", tc.source+"::hstore") + "::text AS v"
					t.Run(tc.base+"/"+tc.source, func(t *testing.T) { hstoreRecordBoth(t, p, b, c, hstoreRecordText(q, tc.value)) })
				}
				hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "ROW(1,2)", "'f1=>42'::hstore")+"::text AS v", "(42,2)"))
				hstoreRun(t, p, b, c, fixture.Exec("CREATE TYPE hs_record_empty AS ()", 0))
				for _, base := range []string{"NULL::hs_record_empty", "'()'::hs_record_empty"} {
					hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, base, "''::hstore")+"::text AS v", "()"))
				}
				hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "NULL::record", "NULL::hstore")+"::text AS v", nil))
			})
		})
	}
}

func TestReferenceHstoreRecordFieldCodecs(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		for _, q := range []string{
			"CREATE DOMAIN hs_record_plain AS integer",
			"CREATE DOMAIN hs_record_domain AS hs_record_pair",
			"CREATE TYPE hs_record_fields AS (v varchar(2),c char(2),n numeric(4,1),a integer[],r hs_record_pair,d hs_record_plain)",
			"CREATE TABLE hs_record_constraints(i integer NOT NULL CHECK(i>0),n text NOT NULL)",
		} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, operator := range []bool{false, true} {
			call := hstoreRecordCall(operator, "NULL::hs_record_fields", `'v=>xy,c=>x,n=>12.34,a=>"{1,NULL,3}",r=>"(42,nested)",d=>0042'::hstore`)
			q := "SELECT (x).v AS v,(x).c::text AS c,(x).n::text AS n,(x).a::text AS a,(x).r::text AS r,(x).d::integer AS d FROM (SELECT " + call + " AS x) s"
			hstoreRecordBoth(t, p, b, c, fixture.Select(q, []string{"v", "c", "n", "a", "r", "d"}, []string{"VARCHAR", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"}, [][]any{{"xy", "x", "12.3", "{1,NULL,3}", "(42,nested)", int64(42)}}))
			hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT hstore("+call+")::text AS v", `"a"=>"{1,NULL,3}", "c"=>"x ", "d"=>"42", "n"=>"12.3", "r"=>"(42,nested)", "v"=>"xy"`))
			hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "NULL::hs_record_constraints", "'i=>-1,n=>NULL'::hstore")+"::text AS v", "(-1,)"))
			hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "'(7,keep)'::hs_record_domain", "'i=>42'::hstore")+"::text AS v", "(42,keep)"))
			hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT "+hstoreRecordCall(operator, "NULL::hs_record_fields", "'d=>NULL'::hstore")+"::text AS v", "(,,,,,)"))
		}
	})
}

func TestReferenceHstoreRecordLiveFieldNames(t *testing.T) {
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSetup(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE TYPE hs_record_quoted AS ("N" text,"雪" integer,"a""b" text)`, 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_record_live(i integer,obsolete text,n text)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_live VALUES (7,'gone','keep')", 1))
		hstoreRun(t, p, b, c, fixture.Exec("ALTER TABLE hs_record_live DROP COLUMN obsolete", 0))
		for _, operator := range []bool{false, true} {
			q := "SELECT " + hstoreRecordCall(operator, "ROW('upper',7,'old')::hs_record_quoted", `'"N"=>changed,n=>ignored,"雪"=>0042,"a\"b"=>"多字"'::hstore`) + "::text AS v"
			hstoreRecordBoth(t, p, b, c, hstoreRecordText(q, "(changed,42,多字)"))
			q = "SELECT " + hstoreRecordCall(operator, "r", "'i=>42,obsolete=>oops'::hstore") + "::text AS v FROM hs_record_live r"
			hstoreRecordBoth(t, p, b, c, hstoreRecordText(q, "(42,keep)"))
		}
	})
}
