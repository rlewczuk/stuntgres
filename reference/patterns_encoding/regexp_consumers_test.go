package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpConsumers(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE consumers(id INT,s TEXT,p TEXT)`, 0))
		run(fixture.Exec(`INSERT INTO consumers VALUES(1,'aba','a'),(2,'x','a'),(3,NULL,'[')`, 3))
		for _, c := range []struct {
			q            string
			names, types []string
			rows         [][]any
		}{
			{`SELECT regexp_count(s,p) AS n,regexp_instr(s,p) AS i,regexp_like(s,p) AS m,regexp_match(s,p) AS a,regexp_replace(s,p,'X','g') AS r,regexp_split_to_array(s,p) AS fields FROM consumers ORDER BY id`, []string{"n", "i", "m", "a", "r", "fields"}, []string{"INT4", "INT4", "BOOL", "_TEXT", "TEXT", "_TEXT"}, [][]any{{int64(2), int64(1), true, "{a}", "XbX", `{"",b,""}`}, {int64(0), int64(0), false, nil, "x", "{x}"}, {nil, nil, nil, nil, nil, nil}}},
			{`SELECT regexp_count(s,p) AS n FROM consumers WHERE regexp_like(s,p) ORDER BY id`, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}},
			{`SELECT CASE WHEN regexp_like(s,p) THEN regexp_replace(s,p,'X','g') ELSE 'no' END AS r FROM consumers ORDER BY id`, []string{"r"}, []string{"TEXT"}, [][]any{{"XbX"}, {"no"}, {"no"}}},
			{`SELECT COALESCE(regexp_match(s,p),ARRAY['missing']) AS a FROM consumers ORDER BY id`, []string{"a"}, []string{"_TEXT"}, [][]any{{"{a}"}, {"{missing}"}, {"{missing}"}}},
			{`SELECT DISTINCT regexp_like(s,p) AS m FROM consumers ORDER BY m`, []string{"m"}, []string{"BOOL"}, [][]any{{false}, {true}, {nil}}},
			{`SELECT regexp_like(s,p) AS m,count(*) AS n FROM consumers GROUP BY regexp_like(s,p) HAVING regexp_like(s,p)`, []string{"m", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}}},
			{`SELECT regexp_instr(a.s,b.p) AS n FROM consumers a JOIN consumers b ON a.id=b.id ORDER BY a.id`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(0)}, {nil}}},
			{`SELECT min(regexp_replace(s,p,'X','g')) AS r FROM consumers`, []string{"r"}, []string{"TEXT"}, [][]any{{"XbX"}}},
		} {
			s := fixture.Select(c.q, c.names, c.types, c.rows)
			regexpRoutes(t, p, b, s)
		}
		run(fixture.Exec(`CREATE TABLE copied(a TEXT[],s TEXT)`, 0))
		run(fixture.Exec(`INSERT INTO copied SELECT regexp_match(s,p),regexp_replace(s,p,'X') FROM consumers WHERE regexp_like(s,p)`, 1))
		regexpRoutes(t, p, b, fixture.Select(`SELECT a,s FROM copied`, []string{"a", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{"{a}", "Xba"}}))
		run(fixture.Exec(`DELETE FROM consumers WHERE regexp_count(s,p)=0`, 1))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec(`UPDATE consumers SET s='aba' WHERE id=1`, 1))
			s := fixture.Select(`UPDATE consumers SET s=regexp_replace(s,p,'X') WHERE id=1 RETURNING regexp_count(s,'X') AS n,regexp_match(s,'X') AS a`, []string{"n", "a"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{X}"}})
			s.Prepared = prepared
			run(s)
		}
	})
}

const regexpStoredDDL = `CREATE TABLE stored(id INT,s TEXT,p TEXT DEFAULT 'a',n INT DEFAULT regexp_count('aba','a'),i INT DEFAULT regexp_instr('é🙂x','🙂'),m BOOL DEFAULT regexp_like('Ab','ab','i'),a TEXT[] DEFAULT regexp_match('x','(a)|(x)'),r TEXT DEFAULT regexp_replace('ab','','X','g'),f TEXT[] DEFAULT regexp_split_to_array('a,,b,',','),g TEXT[] GENERATED ALWAYS AS (regexp_match(s,p)) STORED,CONSTRAINT regexp_valid CHECK(regexp_count(s,p)>=0))`
const regexpStoredQuery = `SELECT n,i,m,a,r,f,g FROM stored ORDER BY id`

func regexpStoredWant() fixture.Step {
	return fixture.Select(regexpStoredQuery, []string{"n", "i", "m", "a", "r", "f", "g"}, []string{"INT4", "INT4", "BOOL", "_TEXT", "TEXT", "_TEXT", "_TEXT"}, [][]any{{int64(2), int64(2), true, "{NULL,x}", "XaXbX", `{a,"",b,""}`, "{a}"}, {int64(2), int64(2), true, "{NULL,x}", "XaXbX", `{a,"",b,""}`, nil}})
}

func TestReferenceRegexpStoredDefinitions(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		regexpStoredOverloads(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		// Domain CHECK uses the same regexp provider and conversion path.
		run(fixture.Exec(`CREATE DOMAIN regexp_constrained AS TEXT CHECK(regexp_like(VALUE,'^a'))`, 0))
		regexpRoutes(t, p, b, fixture.Select(`SELECT regexp_count('aba'::regexp_constrained,'a') AS n,regexp_count(NULL::regexp_constrained,'a') AS missing`, []string{"n", "missing"}, []string{"INT4", "INT4"}, [][]any{{int64(2), nil}}))
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain regexp_constrained violates check constraint "regexp_constrained_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "regexp_constrained", "regexp_constrained_check"
		failure := fixture.ErrorDiagnostic(`SELECT regexp_count('x'::regexp_constrained,'a')`, d)
		failure.SelectRows, failure.ErrorStage = true, "execute"
		regexpRoutes(t, p, b, failure)
		run(fixture.Exec(`CREATE DOMAIN regexp_text AS TEXT`, 0))
		run(fixture.Exec(regexpStoredDDL, 0))
		run(fixture.Exec(`INSERT INTO stored(id,s) VALUES(1,'aba'),(2,NULL)`, 2))
		run(fixture.Exec(`CREATE INDEX regexp_index ON stored((regexp_replace(s,p,'X'))) WHERE regexp_like(s,p)`, 0))
		run(fixture.Exec(`CREATE VIEW scalar_view AS SELECT id,regexp_count(s,p) AS n,regexp_instr(s,p) AS i,regexp_like(s,p) AS m,regexp_match(s,p) AS a,regexp_replace(s,p,'X') AS r,regexp_split_to_array(s,p) AS f FROM stored`, 0))
		run(fixture.Exec(`CREATE MATERIALIZED VIEW regexp_mv AS SELECT regexp_match(s,p) AS a FROM stored`, 2))
		regexpRoutes(t, p, b, regexpStoredWant())
		regexpRoutes(t, p, b, fixture.Select(`SELECT n,i,m,a,r,f FROM scalar_view ORDER BY id`, []string{"n", "i", "m", "a", "r", "f"}, []string{"INT4", "INT4", "BOOL", "_TEXT", "TEXT", "_TEXT"}, [][]any{{int64(2), int64(1), true, "{a}", "Xba", `{"",b,""}`}, {nil, nil, nil, nil, nil, nil}}))
		regexpRoutes(t, p, b, regexpOne(b, `regexp_match('aba'::regexp_text,'a')`, "_TEXT", "{a}"))
		// Actual stored-expression inquiry, including canonical inserted type casts.
		for _, c := range []struct {
			ordinal int
			expr    string
		}{
			{4, `regexp_count('aba'::text, 'a'::text)`}, {5, `regexp_instr('é🙂x'::text, '🙂'::text)`}, {6, `regexp_like('Ab'::text, 'ab'::text, 'i'::text)`},
			{7, `regexp_match('x'::text, '(a)|(x)'::text)`}, {8, `regexp_replace('ab'::text, ''::text, 'X'::text, 'g'::text)`}, {9, `regexp_split_to_array('a,,b,'::text, ','::text)`}, {10, `regexp_match(s, p)`},
		} {
			// These are concrete ordinals from regexpStoredDDL, not a
			// dependency on the unimplemented pg_attribute catalog.
			q := `SELECT pg_get_expr(d.adbin,d.adrelid) AS expression FROM pg_attrdef d WHERE d.adrelid='stored'::regclass::oid AND d.adnum=$1`
			regexpRoutes(t, p, b, fixture.Select(q, []string{"expression"}, []string{"TEXT"}, [][]any{{c.expr}}, c.ordinal))
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, regexpStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: regexpStoredQuery}
		run(fixture.Exec(`ALTER TABLE stored RENAME COLUMN s TO source`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpStoredWant())
		run(fixture.Exec(`ALTER TABLE stored RENAME COLUMN p TO pattern`, 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpStoredWant())
		run(fixture.Exec(`UPDATE stored SET source='aa' WHERE id=1`, 1))
		want := regexpStoredWant()
		want.Rows[0][6] = "{a}"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
	})
}

func TestReferenceRegexpAtomicity(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) {
			if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }) {
				t.FailNow()
			}
		}
		run(fixture.Exec(`CREATE TABLE atomic_t(id INT,s TEXT,p TEXT,sentinel INT)`, 0))
		run(fixture.Exec(`INSERT INTO atomic_t VALUES(1,'a','a',7),(2,'a','[',7)`, 2))
		run(fixture.Exec(`CREATE TABLE atomic_out(s TEXT,a TEXT[])`, 0))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{
				`UPDATE atomic_t SET sentinel=regexp_count(s,p)`,
				`UPDATE atomic_t SET sentinel=regexp_instr(s,p)`,
				`UPDATE atomic_t SET sentinel=99 WHERE regexp_like(s,p)`,
				`UPDATE atomic_t SET sentinel=99 WHERE regexp_match(s,p) IS NOT NULL`,
				`UPDATE atomic_t SET s=regexp_replace(s,p,'X')`,
				`UPDATE atomic_t SET sentinel=99 WHERE regexp_split_to_array(s,p) IS NOT NULL`,
				`INSERT INTO atomic_out(s) SELECT regexp_split_to_table(s,p) FROM atomic_t`,
				`INSERT INTO atomic_out(a) SELECT regexp_matches(s,p) FROM atomic_t`,
			} {
				s := similarReferenceRegexError(q, "brackets [] not balanced", "")
				s.SelectRows = false
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT s,sentinel FROM atomic_t ORDER BY id`, []string{"s", "sentinel"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(7)}, {"a", int64(7)}}))
				run(fixture.Select(`SELECT s,a FROM atomic_out`, []string{"s", "a"}, []string{"TEXT", "_TEXT"}, nil))
			}
		}
		run(fixture.Exec(`CREATE TABLE generated_atomic(s TEXT,p TEXT,g TEXT[] GENERATED ALWAYS AS (regexp_match(s,p)) STORED)`, 0))
		for _, prepared := range []bool{false, true} {
			s := similarReferenceRegexError(`INSERT INTO generated_atomic(s,p) VALUES('a','a'),('a','[')`, "brackets [] not balanced", "")
			s.SelectRows = false
			s.Prepared = prepared
			run(s)
			run(fixture.Select(`SELECT g FROM generated_atomic`, []string{"g"}, []string{"_TEXT"}, nil))
		}
		// SQL conjunction order cannot guard the unrelated malformed second row.
		run(fixture.Exec(`UPDATE atomic_t SET sentinel=8 WHERE id=1 AND regexp_like('a','a')`, 1))
		run(fixture.Select(`SELECT sentinel FROM atomic_t ORDER BY id`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(7)}}))
	})
}

func TestReferenceRegexpControls(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(regexpStoredDDL, 0))
		run(fixture.Exec(`INSERT INTO stored(id,s) VALUES(1,'aba'),(2,NULL)`, 2))
		run(fixture.Exec(`CREATE INDEX regexp_index ON stored((regexp_replace(s,p,'X'))) WHERE regexp_like(s,p)`, 0))
		run(fixture.Exec(`CREATE VIEW set_view AS SELECT id,regexp_matches(s,'a','g') AS m FROM stored`, 0))
		regexpRoutes(t, p, b, regexpStoredWant())
		setWant := fixture.Select(`SELECT id,m FROM set_view ORDER BY id`, []string{"id", "m"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{a}"}, {int64(1), "{a}"}})
		regexpRoutes(t, p, b, setWant)
		if b.PG {
			return
		} // Host replacement APIs have no PostgreSQL counterpart.
		old, err := p.Native.QueryContext(p.Ctx, regexpStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		stmt, err := b.DB.PrepareContext(p.Ctx, regexpStoredQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: regexpStoredQuery}
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
		want := regexpStoredWant()
		fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(setWant)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unstable roundtrip", err)
		}
		bad := dump.String() + `CREATE TABLE late_failure(a TEXT[] DEFAULT regexp_match('a','[')); INSERT INTO late_failure(a) VALUES(ARRAY['ok']); INSERT INTO late_failure DEFAULT VALUES;`
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
		if err == nil {
			t.Fatal("malformed late Restore published")
		}
		diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201B", Message: "invalid regular expression: brackets [] not balanced"}
		fixture.AssertDiagnostic(t, err, &diagnostic)
		run(want)
		run(setWant)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(after.Bytes(), redump.Bytes()) {
			t.Fatal("failed Restore changed destination", err)
		}
		if p.Postgres != nil {
			for _, q := range []string{`DROP VIEW IF EXISTS set_view; DROP TABLE IF EXISTS stored CASCADE`, dump.String()} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, setWant)
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP VIEW set_view; DROP TABLE stored`); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceRegexpFeatureSmoke(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		regexpRoutes(t, p, b, regexpRows(b, `SELECT regexp_count('ABCABC','A.') AS n,regexp_instr('é🙂x','🙂') AS i,regexp_like('Ab','ab','i') AS m,regexp_match('x','(a)|(x)') AS a,regexp_replace('ab','','X','g') AS r,regexp_split_to_array('a,,b,',',') AS f`, []string{"n", "i", "m", "a", "r", "f"}, []string{"INT4", "INT4", "BOOL", "_TEXT", "TEXT", "_TEXT"}, [][]any{{int64(2), int64(2), true, "{NULL,x}", "XaXbX", `{a,"",b,""}`}}))
		regexpRoutes(t, p, b, regexpRows(b, `SELECT m,s FROM ROWS FROM(regexp_matches('aa','a','g'),regexp_split_to_table('x','z')) r(m,s)`, []string{"m", "s"}, []string{"_TEXT", "TEXT"}, [][]any{{"{a}", "x"}, {"{a}", nil}}))
	})
}
