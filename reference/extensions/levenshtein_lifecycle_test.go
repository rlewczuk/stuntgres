package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func levenshteinInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, owner string) {
	t.Helper()
	var ext, ns int64
	var version, namespace, gotOwner string
	var reloc bool
	err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extnamespace,extversion,extrelocatable,nspname,rolname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE extname='fuzzystrmatch'").Scan(&ext, &ns, &version, &reloc, &namespace, &gotOwner)
	if err != nil || ext <= 0 || ns <= 0 || version != "1.2" || !reloc || namespace != "lev_addon" || gotOwner != owner {
		t.Fatal("extension identity", ext, ns, version, reloc, namespace, gotOwner, err)
	}
	expected := map[string]int64{
		"lev_addon.levenshtein(text,text)":                                            2,
		"lev_addon.levenshtein(text,text,integer,integer,integer)":                    5,
		"lev_addon.levenshtein_less_equal(text,text,integer)":                         3,
		"lev_addon.levenshtein_less_equal(text,text,integer,integer,integer,integer)": 6,
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,pronargs,prorettype,provolatile,proisstrict,proparallel,rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE pronamespace=$1 AND (proname='levenshtein' OR proname='levenshtein_less_equal')", ns)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[int64]bool{}
	for rows.Next() {
		var id, nargs, result int64
		var signature, volatility, parallel, memberOwner string
		var strict bool
		if err := rows.Scan(&id, &signature, &nargs, &result, &volatility, &strict, &parallel, &memberOwner); err != nil {
			t.Fatal(err)
		}
		want, ok := expected[signature]
		if !ok || want != nargs || ids[id] || id <= 0 || result != 23 || volatility != "i" || !strict || parallel != "s" || memberOwner != owner {
			t.Fatal("procedure identity", id, signature, nargs, result, volatility, strict, parallel, memberOwner)
		}
		delete(expected, signature)
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(expected) != 0 {
		t.Fatal("missing selected identities", expected)
	}
	for id := range ids {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND refclassid=3079 AND refobjid=$2 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, id, ext))
	}
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}, ext))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, metaphoneSelect("SELECT lev_addon.soundex('John')", "soundex", "J500"))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, levenshteinSelect("SELECT lev_addon.difference('Ann','Anne')", "difference", int64(4)))
	}
}

func TestReferenceLevenshteinInstallation(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		count := func(n int64) {
			run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='levenshtein' OR proname='levenshtein_less_equal'", []string{"count"}, []string{"INT8"}, [][]any{{n}}))
		}
		run(extensionMissing("SELECT levenshtein('kitten','sitting')", "levenshtein", "unknown, unknown"))
		count(0)
		run(fixture.Exec("CREATE SCHEMA lev_addon", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT lev_before", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA lev_addon VERSION '1.2'", 0))
		count(4)
		run(levenshteinSelect("SELECT lev_addon.levenshtein('kitten','sitting')", "levenshtein", int64(3)))
		run(fixture.Exec("ROLLBACK TO lev_before", 0))
		count(0)
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA lev_addon VERSION '1.2'", 0))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		levenshteinInventory(t, p, b, c, owner)
		run(extensionMissing("SELECT levenshtein('a','b')", "levenshtein", "unknown, unknown"))
		run(fixture.Prepared(levenshteinSelect("SELECT lev_addon.levenshtein_less_equal('kitten','sitting',3)", "levenshtein_less_equal", int64(3))))
		run(fixture.Exec("SET search_path TO lev_addon", 0))
		run(levenshteinSelect("SELECT levenshtein('a','b',2,3,7)", "levenshtein", int64(5)))
		run(fixture.Exec("SET search_path TO public", 0))
		run(fixture.Exec("DROP SCHEMA lev_addon CASCADE", 0))
		count(0)
		run(fixture.ImplicitError("SELECT lev_addon.levenshtein('a','b')", fixture.Diagnostic{Code: "3F000", Message: `schema "lev_addon" does not exist`, Position: "8"}))
	})
}

func levenshteinStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA lev_addon", 0))
	run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA lev_addon VERSION '1.2'", 0))
	run(fixture.Exec("CREATE TABLE lev_source(id int,src text,dst text,d int DEFAULT lev_addon.levenshtein('kitten','sitting'),g int GENERATED ALWAYS AS (lev_addon.levenshtein(src,dst,2,3,4)) STORED,CONSTRAINT lev_limit CHECK(lev_addon.levenshtein_less_equal(src,dst,3)>=0))", 0))
	run(fixture.Exec("INSERT INTO lev_source(id,src,dst) VALUES(1,'kitten','sitting'),(2,'a','b')", 2))
	run(fixture.Exec("CREATE VIEW lev_values AS SELECT id,d,g,lev_addon.levenshtein(src,dst) AS distance,lev_addon.levenshtein_less_equal(src,dst,2,3,4,20) AS bounded FROM lev_source", 0))
	check := fixture.Select("SELECT id,d,g,distance,bounded FROM lev_values ORDER BY id", []string{"id", "d", "g", "distance", "bounded"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(3), int64(10), int64(3), int64(10)}, {int64(2), int64(3), int64(4), int64(1), int64(4)}})
	run(check)
	run(fixture.Prepared(check))
	return check
}

func TestReferenceLevenshteinStoredExpressions(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := levenshteinStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lev_checked(id int,src text,dst text,CONSTRAINT lev_within CHECK(lev_addon.levenshtein(src,dst)<=3))", 0))
		run(fixture.Exec("INSERT INTO lev_checked VALUES(1,'kitten','sitting')", 1))
		failure := fixture.ImplicitError("INSERT INTO lev_checked VALUES(2,'a','b'),(3,'aaaa','bbbb')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "lev_checked" violates check constraint "lev_within"`, Detail: "Failing row contains (3, aaaa, bbbb).", Schema: "public", Table: "lev_checked", Constraint: "lev_within"})
		failure.ErrorStage = "execute"
		for _, prepared := range []bool{false, true} {
			failure.Prepared = prepared
			run(failure)
			run(fixture.Select("SELECT id FROM lev_checked ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT lev_addon.levenshtein(src,dst) AS distance,count(*) FROM lev_source GROUP BY lev_addon.levenshtein(src,dst) ORDER BY distance", []string{"distance", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(1)}}),
			fixture.Select("SELECT a.id,b.id FROM lev_source a JOIN lev_source b ON lev_addon.levenshtein(a.src,b.src)=0 ORDER BY a.id,b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			fixture.Select("SELECT id,CASE WHEN id=1 THEN lev_addon.levenshtein(src,dst,2,3,4) ELSE lev_addon.levenshtein_less_equal(src,dst,3) END AS v,lev_addon.levenshtein_less_equal(src,dst,lev_addon.levenshtein(src,dst)) AS nested FROM lev_source ORDER BY id", []string{"id", "v", "nested"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(3)}, {int64(2), int64(1), int64(1)}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		empty := check
		empty.Query = "SELECT id,d,g,distance,bounded FROM lev_values WHERE false"
		empty.Rows = nil
		run(empty)
		run(fixture.Prepared(empty))
		run(fixture.Exec("UPDATE lev_source SET dst='a' WHERE id=2", 1))
		run(fixture.Select("SELECT g,bounded FROM lev_values WHERE id=2", []string{"g", "bounded"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}}))
	})
}
