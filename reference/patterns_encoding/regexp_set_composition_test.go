package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpSetComposition(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		regexpSetWalks(t, p, b)
		for i, c := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{`SELECT regexp_split_to_table('b a',' ') AS x ORDER BY x LIMIT 1`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}}},
			{`SELECT DISTINCT regexp_split_to_table('a a b',' ') AS x ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
			{`SELECT 7 AS n ORDER BY regexp_split_to_table('b a',' ') LIMIT 2`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}},
			{`SELECT regexp_split_to_table('a b',' ') AS x,count(*) AS n FROM (VALUES(1),(2)) v(id) GROUP BY x ORDER BY x`, []string{"x", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(2)}}},
			{`SELECT count(*) AS n,regexp_split_to_table('a b',' ') AS x FROM (VALUES(1),(2)) v(id) ORDER BY x`, []string{"n", "x"}, []string{"INT8", "TEXT"}, [][]any{{int64(2), "a"}, {int64(2), "b"}}},
			{`SELECT row_number() OVER () AS n,regexp_split_to_table('a b',' ') AS x FROM (VALUES(1),(2)) v(id) ORDER BY n,x`, []string{"n", "x"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(2), "a"}, {int64(2), "b"}}},
			{`SELECT row_number() OVER (ORDER BY regexp_split_to_table('b a',' ')) AS n`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}},
			{`SELECT row_number() OVER (PARTITION BY regexp_split_to_table('a b',' ')) AS n`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}}},
			{`SELECT EXISTS(SELECT regexp_matches('x','a')) AS found`, []string{"found"}, []string{"BOOL"}, [][]any{{false}}},
			{`SELECT EXISTS(SELECT regexp_matches('a','a')) AS found`, []string{"found"}, []string{"BOOL"}, [][]any{{true}}},
			{`WITH s AS (SELECT regexp_split_to_table('a b',' ') AS x) SELECT x FROM s ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
			{`SELECT x FROM (SELECT regexp_split_to_table('a b',' ') AS x) s ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
			{`SELECT regexp_split_to_table('a b',' ') AS x UNION SELECT regexp_split_to_table('b c',' ') AS x ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}}},
			{`SELECT regexp_split_to_table('a b',' ') AS x UNION ALL SELECT regexp_split_to_table('b c',' ') AS x ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"b"}, {"c"}}},
			{`SELECT DISTINCT ON(regexp_split_to_table('b a a',' ')) regexp_split_to_table('b a a',' ') AS x ORDER BY x`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
		} {
			s := regexpRows(b, c.q, c.names, c.types, c.rows)
			// These five rows expose a direct COUNT or ROW_NUMBER output;
			// native already knows it is NOT NULL, while pq exposes unknown.
			if i >= 3 && i <= 7 {
				s.CheckMetadata = regexpMetadata(b.PG, "n")
			}
			if i == 6 {
				// Window-key ordering is not an outer result ORDER BY.
				s.Bag = true
			}
			regexpRoutes(t, p, b, s)
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE inserted(s TEXT)`, 0))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec(`DELETE FROM inserted`, map[bool]int64{false: 0, true: 4}[prepared]))
			s := fixture.Exec(`INSERT INTO inserted SELECT regexp_split_to_table('a b',' ')`, 2)
			s.Prepared = prepared
			run(s)
			s = fixture.Exec(`INSERT INTO inserted VALUES(regexp_split_to_table('c d',' '))`, 2)
			s.Prepared = prepared
			run(s)
			s = regexpRows(b, `SELECT s FROM inserted ORDER BY s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}, {"d"}})
			s.CheckMetadata = regexpMetadataNullable(b.PG, []string{"s"})
			run(s)
		}
	})
}

func TestReferenceRegexpSetPlacement(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE placement(v TEXT,n INT)`, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO placement VALUES('a',1)`, 1))
		for _, c := range []struct{ q, context string }{
			{`SELECT 1 WHERE regexp_split_to_table('a','a')='a'`, "WHERE"},
			{`SELECT count(*) FROM placement HAVING regexp_split_to_table('a','a')='a'`, "HAVING"},
			{`SELECT count(*) FILTER(WHERE regexp_split_to_table('a','a')='a') FROM placement`, "FILTER"},
			{`SELECT * FROM placement p JOIN placement q ON regexp_split_to_table('a','a')='a'`, "JOIN conditions"},
			{`SELECT 1 LIMIT regexp_split_to_table('1','a')::int`, "LIMIT"},
			{`SELECT 1 OFFSET regexp_split_to_table('1','a')::int`, "OFFSET"},
			{`UPDATE placement SET v=regexp_split_to_table('a b',' ')`, "UPDATE"},
			{`UPDATE placement SET v='a' RETURNING regexp_split_to_table('a',' ')`, "RETURNING"},
			{`INSERT INTO placement(v) VALUES('a') RETURNING regexp_split_to_table('a',' ')`, "RETURNING"},
			{`CREATE TABLE rejected(v TEXT CHECK(regexp_split_to_table(v,' ')='a'))`, "check constraints"},
			{`CREATE TABLE rejected(v TEXT DEFAULT regexp_split_to_table('a',' '))`, "DEFAULT expressions"},
			{`CREATE TABLE rejected(v TEXT,g TEXT GENERATED ALWAYS AS (regexp_split_to_table(v,' ')) STORED)`, "column generation expressions"},
			{`CREATE INDEX rejected ON placement((regexp_split_to_table(v,' ')))`, "index expressions"},
			{`CREATE INDEX rejected ON placement(v) WHERE regexp_split_to_table(v,' ')='a'`, "index predicates"},
			{`CREATE POLICY rejected ON placement USING(regexp_split_to_table(v,' ')='a')`, "policy expressions"},
			{`VALUES(regexp_split_to_table('a b',' '))`, "VALUES"},
			{`INSERT INTO placement(v) VALUES(regexp_split_to_table('a b',' ')),('c')`, "VALUES"},
			{`SELECT count(*) OVER (ROWS regexp_split_to_table('1',' ')::int PRECEDING) FROM placement`, "window definitions"},
		} {
			q := c.q
			if len(q) >= 6 && q[:6] == "SELECT" {
				q = "SELECT /* é🙂 */" + q[6:]
			}
			s := regexpBindError(q, "0A000", "set-returning functions are not allowed in "+c.context, "", "regexp_split_to_table")
			if c.context == "UPDATE" || c.context == "RETURNING" || c.context == "check constraints" || c.context == "DEFAULT expressions" || c.context == "column generation expressions" || c.context == "index expressions" || c.context == "index predicates" || c.context == "policy expressions" || c.context == "VALUES" {
				s.SelectRows = false
			}
			// Utility expressions are transformed by execution, not protocol Parse.
			if len(q) >= 6 && q[:6] == "CREATE" {
				s.ErrorStage = "execute"
			}
			if c.context == "policy expressions" {
				s.Diagnostic.Position = ""
			}
			regexpRoutes(t, p, b, s)
		}
		lateralHint := "You might be able to move the set-returning function into a LATERAL FROM item."
		for _, c := range []struct{ q, msg, hint string }{
			{`SELECT CASE WHEN false THEN regexp_split_to_table('a',' ') ELSE 'b' END`, `set-returning functions are not allowed in CASE`, lateralHint},
			{`SELECT COALESCE('safe',regexp_split_to_table('a',' '))`, `set-returning functions are not allowed in COALESCE`, lateralHint},
			{`SELECT count(regexp_split_to_table('a b',' '))`, `aggregate function calls cannot contain set-returning function calls`, lateralHint},
			{`SELECT count(regexp_split_to_table('a b',' ')) OVER ()`, `window function calls cannot contain set-returning function calls`, lateralHint},
			{`SELECT first_value(regexp_split_to_table('a b',' ')) OVER ()`, `window function calls cannot contain set-returning function calls`, lateralHint},
			{`SELECT /* é🙂 */ sum(n) OVER (ROWS regexp_split_to_table('1',' ')::int PRECEDING) FROM placement`, "set-returning functions are not allowed in window definitions", ""},
		} {
			regexpRoutes(t, p, b, regexpBindError(c.q, "0A000", c.msg, c.hint, "regexp_split_to_table"))
		}
		for _, strength := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
			q := `SELECT regexp_matches(v,'a') FROM placement FOR ` + strength
			regexpRoutes(t, p, b, similarReferenceBindingError(q, fixture.Diagnostic{Code: "0A000", Message: "FOR " + strength + " is not allowed with set-returning functions in the target list"}))
			q = `SELECT * FROM regexp_split_to_table('a b',' ') r FOR ` + strength + ` OF r`
			regexpRoutes(t, p, b, similarReferenceBindingError(q, fixture.Diagnostic{Code: "0A000", Message: "FOR " + strength + " cannot be applied to a function", Position: fixture.Position(len(q))}))
		}
		locked := regexpRows(b, `SELECT p.v,r.s FROM placement p,regexp_split_to_table(p.v,' ') r(s) FOR UPDATE OF p`, []string{"v", "s"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}})
		locked.CheckMetadata = regexpMetadataNullable(b.PG, []string{"v"})
		regexpRoutes(t, p, b, locked)
		// Both selected SRFs get the same placement policy, even for array-valued output.
		q := `SELECT 1 WHERE regexp_matches('a','a') IS NOT NULL`
		regexpRoutes(t, p, b, regexpBindError(q, "0A000", `set-returning functions are not allowed in WHERE`, "", "regexp_matches"))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`PREPARE regexp_scalar(text) AS SELECT regexp_like($1,'a')`, 0))
		for _, prepared := range []bool{false, true} {
			s := regexpBindError(`EXECUTE regexp_scalar(regexp_split_to_table('a',' '))`, "0A000", `set-returning functions are not allowed in EXECUTE parameters`, "", "regexp_split_to_table")
			s.SelectRows = false
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(`DEALLOCATE regexp_scalar`, 0))
	})
}

func TestReferenceRegexpSetViews(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE view_source(id INT,s TEXT)`, 0))
		run(fixture.Exec(`INSERT INTO view_source VALUES(1,'a b'),(2,NULL)`, 2))
		run(fixture.Exec(`CREATE VIEW regexp_view AS SELECT id,r.s,r.o FROM view_source v LEFT JOIN LATERAL regexp_split_to_table(v.s,' ') WITH ORDINALITY r(s,o) ON true`, 0))
		want := fixture.Select(`SELECT id,s,o FROM regexp_view ORDER BY id,o`, []string{"id", "s", "o"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(1), "b", int64(2)}, {int64(2), nil, nil}})
		regexpRoutes(t, p, b, want)
		run(fixture.Exec(`ALTER TABLE view_source RENAME COLUMN s TO source`, 0))
		regexpRoutes(t, p, b, want)
		run(fixture.Exec(`ALTER TABLE view_source RENAME TO renamed_source`, 0))
		regexpRoutes(t, p, b, want)
		// Public stored definition inquiry must retain selected syntax and dependencies.
		// Read the inquiry itself, with an independently specified complete
		// definition, rather than adding unsupported inquiry-LIKE syntax.
		definition := " SELECT v.id,\n    r.s,\n    r.o\n   FROM renamed_source v\n     LEFT JOIN LATERAL regexp_split_to_table(v.source, ' '::text) WITH ORDINALITY r(s, o) ON true;"
		regexpRoutes(t, p, b, fixture.DeparseRelationsTextStep(t, b, `SELECT pg_get_viewdef('regexp_view'::regclass,true) AS definition`, definition))
	})
}
