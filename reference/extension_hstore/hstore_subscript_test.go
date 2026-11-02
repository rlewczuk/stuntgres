package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// hstoreSubscriptSeed distinguishes present, NULL-valued and missing keys from
// a NULL container.
func hstoreSubscriptSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_sub(id integer, h hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_sub VALUES (1,'a=>1, b=>NULL, "q k"=>x'),(2,NULL),(3,'')`, 3))
}

func hstoreSubscriptRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, rows [][]any) {
	t.Helper()
	s := hstoreSelect(b, "SELECT id,h AS v FROM hs_sub ORDER BY id", []string{"id", "v"}, rows, []bool{false, true})
	hstoreRun(t, p, b, c, s)
}

func TestReferenceHstoreSubscriptFetch(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreSubscriptSeed(t, p, b, c)
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT id,h['a'] AS a,h['b'] AS b,h['zz'] AS zz,h[NULL] AS n,h['q k'] AS qk FROM hs_sub ORDER BY id`, []string{"id", "a", "b", "zz", "n", "qk"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "1", nil, nil, nil, "x"}, {int64(2), nil, nil, nil, nil, nil}, {int64(3), nil, nil, nil, nil, nil}}),
			fixture.Select(`SELECT h['a'] FROM hs_sub WHERE id=1`, []string{"h"}, []string{"TEXT"}, [][]any{{"1"}}),
			fixture.Select(`SELECT ('1=>one'::hstore)[1] AS i,('1=>one'::hstore)[1::bigint] AS b,('"(1,2)"=>r'::hstore)[ROW(1,2)] AS r,('a=>1'::hstore)['a']`, []string{"i", "b", "r", "hstore"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"one", "one", "r", "1"}}),
			fixture.Select(`SELECT id FROM hs_sub WHERE h['a']='1' OR h['zz'] IS NOT NULL`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT pg_typeof(h['a'])::text AS t FROM hs_sub WHERE id=1`, []string{"t"}, []string{"TEXT"}, [][]any{{"text"}}),
			fixture.Select(`SELECT h['a'] AS a FROM hs_sub WHERE false`, []string{"a"}, []string{"TEXT"}, nil),
		} {
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
		// An unknown bound key resolves to text, the handler's subscript type.
		q := `SELECT h[$1] AS v FROM hs_sub WHERE id=$2`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			key  any
			id   int64
			want any
		}{{"a", 1, "1"}, {nil, 1, nil}, {"q k", 1, "x"}, {"b", 1, nil}, {"a", 2, nil}} {
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.key, tc.id)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
			hstoreRun(t, p, b, held, s)
		}
		// A domain over hstore is reduced to its base before subscripting.
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE DOMAIN hs_sub_dom AS hstore CHECK (length(VALUE::text) < 30)`, 0))
		s := fixture.Select(`SELECT ('must=>1'::hs_sub_dom)['must'] AS m,pg_typeof(('must=>1'::hs_sub_dom)['must'])::text AS t`, []string{"m", "t"}, []string{"TEXT", "TEXT"}, [][]any{{"1", "text"}})
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		s = fixture.Select(`UPDATE hs_sub SET id=id WHERE id=1 RETURNING h['q k'] AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"x"}})
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
	})
}

func TestReferenceHstoreSubscriptDiagnostics(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreSubscriptSeed(t, p, b, c)
		single := fixture.ControlsReferenceDiagnostic("0A000", "hstore allows only one subscript")
		text := fixture.ControlsReferenceDiagnostic("42804", "cannot subscript type text because it does not support subscripting")
		text.Position = "9"
		null := fixture.ControlsReferenceDiagnostic("22004", "hstore subscript in assignment must not be null")
		for _, tc := range []struct {
			q                string
			query, atPrepare bool
			want             fixture.Diagnostic
		}{
			{`SELECT h['a']['b'] FROM hs_sub`, true, true, single},
			{`SELECT h['a':'b'] FROM hs_sub`, true, true, single},
			{`SELECT h[:'b'] FROM hs_sub`, true, true, single},
			{`SELECT (h['a'])['b'] FROM hs_sub`, true, true, text},
			{`UPDATE hs_sub SET h['a']['b']='v' WHERE id=1`, false, true, single},
			{`UPDATE hs_sub SET h['a':'b']='v' WHERE id=1`, false, true, single},
			{`UPDATE hs_sub SET h[NULL]='v' WHERE id=1`, false, false, null},
			{`INSERT INTO hs_sub(id,h[NULL]) VALUES (9,'v')`, false, false, null},
		} {
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, tc.q, prepared, tc.query, tc.atPrepare, tc.want)
			}
		}
		// The assigned value is evaluated before the NULL-subscript check.
		division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, `UPDATE hs_sub SET h[NULL]=(1/(id-1))::text WHERE id=1`, prepared, false, false, division)
			hstoreError(t, p, b, c, `UPDATE hs_sub SET h[$1]=$2 WHERE id=1`, prepared, false, false, null, nil, "v")
		}
		hstoreSubscriptRows(t, p, b, c, [][]any{{int64(1), `"a"=>"1", "b"=>NULL, "q k"=>"x"`}, {int64(2), nil}, {int64(3), ""}})
	})
}

func TestReferenceHstoreSubscriptAssignment(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreSubscriptSeed(t, p, b, c)
		// Every right-hand side reads the original row; a NULL container is empty.
		hstoreRun(t, p, b, c, fixture.Exec(`UPDATE hs_sub SET h['d']=h['q k'],h['x']='xyzzy'`, 3))
		hstoreSubscriptRows(t, p, b, c, [][]any{{int64(1), `"a"=>"1", "b"=>NULL, "d"=>"x", "x"=>"xyzzy", "q k"=>"x"`}, {int64(2), `"d"=>NULL, "x"=>"xyzzy"`}, {int64(3), `"d"=>NULL, "x"=>"xyzzy"`}})
		s := hstoreSelect(b, `UPDATE hs_sub SET h['a']=7 WHERE id=2 RETURNING id,h AS v`, []string{"id", "v"}, [][]any{{int64(2), `"a"=>"7", "d"=>NULL, "x"=>"xyzzy"`}}, []bool{false, true})
		hstoreRun(t, p, b, c, s)
		s = hstoreSelect(b, `UPDATE hs_sub SET h['a']=ARRAY[1,2] WHERE id=2 RETURNING id,h AS v`, []string{"id", "v"}, [][]any{{int64(2), `"a"=>"{1,2}", "d"=>NULL, "x"=>"xyzzy"`}}, []bool{false, true})
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		s = hstoreSelect(b, `INSERT INTO hs_sub(id,h['k'],h['j']) VALUES (4,'kv',NULL) RETURNING id,h AS v`, []string{"id", "v"}, [][]any{{int64(4), `"j"=>NULL, "k"=>"kv"`}}, []bool{false, true})
		hstoreRun(t, p, b, c, s)
		s = hstoreSelect(b, `INSERT INTO hs_sub(id,h['k']) VALUES (5,ROW(1,2)) RETURNING id,h AS v`, []string{"id", "v"}, [][]any{{int64(5), `"k"=>"(1,2)"`}}, []bool{false, true})
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		q := `UPDATE hs_sub SET h[$1]=$2 WHERE id=$3 RETURNING id,h AS v`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"id", "v"}, [][]any{{int64(3), `"d"=>NULL, "x"=>"xyzzy", "pk"=>"pv"`}}, []bool{false, true}, "pk", "pv", 3))
		hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"id", "v"}, [][]any{{int64(3), `"d"=>NULL, "x"=>"xyzzy", "pk"=>NULL`}}, []bool{false, true}, "pk", nil, 3))
		// Assignment through a domain keeps the domain.
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE DOMAIN hs_sub_dom AS hstore CHECK (length(VALUE::text) < 30)`, 0))
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE TABLE hs_sub_domain(d hs_sub_dom)`, 0))
		hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_sub_domain VALUES ('must=>1')`, 1))
		hstoreRun(t, p, b, c, fixture.Select(`UPDATE hs_sub_domain SET d['x']='2' RETURNING d::text AS d,d['x'] AS x`, []string{"d", "x"}, []string{"TEXT", "TEXT"}, [][]any{{`"x"=>"2", "must"=>"1"`, "2"}}))
		hstoreRun(t, p, b, c, fixture.Select(`SELECT d::text AS d FROM hs_sub_domain`, []string{"d"}, []string{"TEXT"}, [][]any{{`"x"=>"2", "must"=>"1"`}}))
	})
}

func TestReferenceHstoreSubscriptPLpgSQL(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE FUNCTION hs_sub_pl() RETURNS text LANGUAGE plpgsql AS $$ declare h hstore; begin h['a'] := 'b'; h['c'] := NULL; return h::text || '|' || h['a']; end $$`, 0))
		hstoreRun(t, p, b, c, fixture.Exec(`CREATE FUNCTION hs_sub_pl_null() RETURNS text LANGUAGE plpgsql AS $$ declare h hstore; k text; begin h[k] := 'b'; return h::text; end $$`, 0))
		s := fixture.Select(`SELECT hs_sub_pl() AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"b", "c"=>NULL|b`}})
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		want := fixture.ControlsReferenceDiagnostic("22004", "hstore subscript in assignment must not be null")
		want.Context = "PL/pgSQL function hs_sub_pl_null() line 1 at assignment"
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, `SELECT hs_sub_pl_null()`, prepared, true, false, want)
		}
	})
}
