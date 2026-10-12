package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Presence is an occurrence property, not an inference from NULL cells or width.
// A present all-NULL or zero-field source remains a non-NULL whole-row value.
func TestReferenceCompositeExprWholeRowAbsence(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE presence_keys(k integer)`, `CREATE TABLE presence_left(i integer)`, `CREATE TABLE presence_right(t text)`, `CREATE TABLE presence_full(i integer)`)
		for _, s := range []fixture.Step{
			fixture.Exec(`INSERT INTO presence_keys VALUES(1),(2)`, 2),
			fixture.Exec(`INSERT INTO holder VALUES(NULL,NULL,NULL)`, 1),
			fixture.Exec(`INSERT INTO presence_left VALUES(NULL)`, 1),
			fixture.Exec(`INSERT INTO presence_right VALUES(NULL)`, 1),
			fixture.Exec(`INSERT INTO presence_full VALUES(1),(NULL)`, 2),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		names := []string{"k", "n", "nn", "whole", "reconstructed"}
		types := []string{"INT4", "INT4", "INT4", "TEXT", "INT4"}
		for _, q := range []string{
			`SELECT a.k,num_nulls(h.*) AS n,num_nonnulls(h.*) AS nn,h.*::text AS whole,num_nulls(ROW(h.*)) AS reconstructed FROM presence_keys a LEFT JOIN holder h ON a.k=1 ORDER BY a.k`,
			`SELECT a.k,num_nulls(h.*) AS n,num_nonnulls(h.*) AS nn,h.*::text AS whole,num_nulls(ROW(h.*)) AS reconstructed FROM holder h RIGHT JOIN presence_keys a ON a.k=1 ORDER BY a.k`,
		} {
			compositeExprBoth(t, p, b, c, q, names, types, [][]any{{int64(1), int64(0), int64(1), "(,,)", int64(0)}, {int64(2), int64(1), int64(0), nil, int64(0)}})
		}
		// VALUES, ordinary derived, and materialized derived sources each establish
		// their own tuple presence; an all-NULL child output must not become absent.
		for _, source := range []struct{ sql, whole string }{
			{`(VALUES(NULL::integer,NULL::text)) AS d(i,t)`, "(,)"},
			{`(SELECT NULL::integer AS i,NULL::text AS t) AS d`, "(,)"},
			{`(SELECT DISTINCT NULL::integer AS i,NULL::text AS t FROM presence_keys) AS d`, "(,)"},
			{`(SELECT max(k) AS i FROM holder) AS d`, "()"},
			{`(SELECT NULL::integer AS i UNION SELECT NULL::integer AS i) AS d`, "()"},
		} {
			q := `SELECT a.k,num_nulls(d.*) AS n,num_nonnulls(d.*) AS nn,d.*::text AS whole,num_nulls(ROW(d.*)) AS reconstructed FROM presence_keys a LEFT JOIN ` + source.sql + ` ON a.k=1 ORDER BY a.k`
			compositeExprBoth(t, p, b, c, q, names, types, [][]any{{int64(1), int64(0), int64(1), source.whole, int64(0)}, {int64(2), int64(1), int64(0), nil, int64(0)}})
		}
		// The emitted join-alias tuple is present even when its right child is
		// absent. Only null-extension by an enclosing join makes that tuple absent.
		compositeExprBoth(t, p, b, c, `SELECT a.k,num_nulls(j.*) AS n,num_nonnulls(j.*) AS nn,j.*::text AS whole,num_nulls(ROW(j.*)) AS reconstructed FROM presence_keys a LEFT JOIN (presence_left p LEFT JOIN presence_right r ON false) AS j ON a.k=1 ORDER BY a.k`, names, types, [][]any{{int64(1), int64(0), int64(1), "(,)", int64(0)}, {int64(2), int64(1), int64(0), nil, int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT a.k,num_nulls(z.*) AS n,num_nonnulls(z.*) AS nn,z.*::text AS whole,num_nulls(ROW(z.*)) AS reconstructed FROM presence_keys a LEFT JOIN (SELECT (ROW()).*) AS z ON a.k=1 ORDER BY a.k`, names, types, [][]any{{int64(1), int64(0), int64(1), "()", int64(0)}, {int64(2), int64(1), int64(0), nil, int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT a.k,num_nulls(p.*) AS n,num_nonnulls(p.*) AS nn,p.*::text AS whole,num_nulls(a.*) AS left_n FROM presence_keys a FULL JOIN presence_full p ON a.k=p.i ORDER BY coalesce(a.k,3)`, []string{"k", "n", "nn", "whole", "left_n"}, types, [][]any{{int64(1), int64(0), int64(1), "(1)", int64(0)}, {int64(2), int64(1), int64(0), nil, int64(0)}, {nil, int64(0), int64(1), "()", int64(1)}})
		// Demand survives qualification, cached ordering, window overlay, DISTINCT
		// ON selection, aggregate input, and a correlated outer evaluation frame.
		for _, q := range []string{
			`SELECT a.k,num_nulls(h.*) AS n,h.*::text AS whole FROM presence_keys a LEFT JOIN holder h ON a.k=1 WHERE a.k=2`,
			`SELECT a.k,num_nulls(h.*) AS n,h.*::text AS whole FROM presence_keys a LEFT JOIN holder h ON a.k=1 ORDER BY a.k DESC LIMIT 1`,
			`SELECT DISTINCT ON (a.k) a.k,num_nulls(h.*) AS n,h.*::text AS whole FROM presence_keys a LEFT JOIN holder h ON a.k=1 WHERE a.k=2 ORDER BY a.k`,
			`SELECT a.k,(SELECT num_nulls(h.*)) AS n,(SELECT h.*::text) AS whole FROM presence_keys a LEFT JOIN holder h ON a.k=1 WHERE a.k=2`,
		} {
			compositeExprBoth(t, p, b, c, q, []string{"k", "n", "whole"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(1), nil}})
		}
		compositeExprBoth(t, p, b, c, `SELECT a.k,row_number() OVER (ORDER BY a.k) AS r,num_nulls(h.*) AS n,h.*::text AS whole FROM presence_keys a LEFT JOIN holder h ON a.k=1 ORDER BY a.k`, []string{"k", "r", "n", "whole"}, []string{"INT4", "INT8", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), int64(0), "(,,)"}, {int64(2), int64(2), int64(1), nil}})
		compositeExprBoth(t, p, b, c, `SELECT max(num_nulls(h.*)) AS n FROM presence_keys a LEFT JOIN holder h ON a.k=1`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE presence_target(k integer,v pair)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO presence_target VALUES(1,ROW(7,'old')),(2,ROW(8,'old'))`, 2))
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec(`UPDATE presence_target t SET v.i=num_nulls(h.*),v.t='upd' FROM presence_keys a LEFT JOIN holder h ON a.k=1 WHERE t.k=a.k`, 2)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			compositeExprBoth(t, p, b, c, `SELECT (v).i,(v).t FROM presence_target ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "upd"}, {int64(1), "upd"}})
			step = fixture.Exec(`MERGE INTO presence_target t USING presence_keys a LEFT JOIN holder h ON a.k=1 ON t.k=a.k WHEN MATCHED THEN UPDATE SET v.i=num_nulls(h.*),v.t='mrg'`, 2)
			step.Prepared = prepared
			fixture.MergeRun(t, p, b, prepared)(step)
			compositeExprBoth(t, p, b, c, `SELECT (v).i,(v).t FROM presence_target ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "mrg"}, {int64(1), "mrg"}})
		}
	})
}

// Scalar aliases and genuine ordinal/multicall tuples retain distinct value,
// presence and frozen replay semantics on direct and prepared consumers.
func TestReferenceCompositeExprFunctionAlias(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprBoth(t, p, b, c,
			`SELECT f AS bare,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,f.v AS qualified FROM string_to_table('a,b',',') f(v) ORDER BY v`,
			[]string{"bare", "scalar_star", "constructed", "qualified"},
			[]string{"TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{{"a", "a", "(a)", "a"}, {"b", "b", "(b)", "b"}})
		compositeExprBoth(t, p, b, c,
			`SELECT f.* FROM string_to_table('a,b',',') f(v) ORDER BY v`,
			[]string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}})
		for _, source := range []string{
			`string_to_table(NULL::text,',')`,
			`string_to_table('NULL',',','NULL')`,
		} {
			compositeExprBoth(t, p, b, c,
				`SELECT f AS bare,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count FROM (VALUES(1)) a(k) LEFT JOIN `+source+` f(v) ON TRUE`,
				[]string{"bare", "scalar_star", "constructed", "null_count"},
				[]string{"TEXT", "TEXT", "TEXT", "INT4"},
				[][]any{{nil, nil, "()", int64(1)}})
		}
		// Ordinality and multiple functions declare a tuple; the scalar-alias
		// correction must not erase their genuine whole-row presence authority.
		compositeExprBoth(t, p, b, c,
			`SELECT f::text AS whole,f.*::text AS scalar_star,ROW(f.*)::text AS constructed FROM string_to_table('a,b',',') WITH ORDINALITY f(v,n) ORDER BY n`,
			[]string{"whole", "scalar_star", "constructed"},
			[]string{"TEXT", "TEXT", "TEXT"},
			[][]any{{"(a,1)", "(a,1)", "(a,1)"}, {"(b,2)", "(b,2)", "(b,2)"}})
		compositeExprBoth(t, p, b, c,
			`SELECT f AS bare,f::text AS whole FROM ROWS FROM(string_to_table('a',',')) f(v)`,
			[]string{"bare", "whole"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "a"}})
		for _, sample := range []struct {
			source string
			whole  any
			nulls  int64
		}{
			{`ROWS FROM(string_to_table(NULL::text,','),string_to_table(NULL::text,','))`, nil, 1},
			{`ROWS FROM(string_to_table('NULL',',','NULL'),string_to_table('NULL',',','NULL'))`, "(,)", 0},
		} {
			compositeExprBoth(t, p, b, c,
				`SELECT f::text AS whole,num_nulls(f) AS null_count FROM (VALUES(1)) a(k) LEFT JOIN `+sample.source+` f(v,w) ON TRUE`,
				[]string{"whole", "null_count"}, []string{"TEXT", "INT4"},
				[][]any{{sample.whole, sample.nulls}})
		}
		// A derived source owns a RECORD even when its child is scalar TEXT.
		compositeExprBoth(t, p, b, c,
			`SELECT d::text AS derived FROM (SELECT v FROM string_to_table('a',',') f(v)) d`,
			[]string{"derived"}, []string{"TEXT"}, [][]any{{"(a)"}})
		// Exposed columns still precede an identically named source alias.
		compositeExprBoth(t, p, b, c,
			`SELECT f FROM string_to_table('a',',') f(v),(VALUES(9)) s(f)`,
			[]string{"f"}, []string{"INT4"}, [][]any{{int64(9)}})
		compositeExprError(t, p, c,
			`SELECT (f).* FROM string_to_table('a',',') f(v)`,
			"42809", "type text is not composite", "")
		compositeExprError(t, p, c,
			`SELECT (f).v FROM string_to_table('a',',') f(v)`,
			"42809", "column notation .v applied to type text, which is not a composite type", "f).v")

		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE function_alias_keys(k integer)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO function_alias_keys VALUES(1)`, 1))
		for _, view := range []struct{ name, source string }{
			{"function_alias_present", `string_to_table('a,b',',')`},
			{"function_alias_absent", `string_to_table(NULL::text,',')`},
			{"function_alias_null", `string_to_table('NULL',',','NULL')`},
		} {
			fixture.CompositeReferenceSetup(t, p, b, c,
				`CREATE VIEW `+view.name+` AS SELECT a.k,f AS bare,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count,f.v AS qualified FROM function_alias_keys a LEFT JOIN `+view.source+` f(v) ON a.k=1`)
		}
		for _, view := range []struct{ name, source string }{
			{"function_tuple_absent", `ROWS FROM(string_to_table(NULL::text,','),string_to_table(NULL::text,','))`},
			{"function_tuple_null", `ROWS FROM(string_to_table('NULL',',','NULL'),string_to_table('NULL',',','NULL'))`},
		} {
			fixture.CompositeReferenceSetup(t, p, b, c,
				`CREATE VIEW `+view.name+` AS SELECT a.k,f::text AS whole,num_nulls(f) AS null_count FROM function_alias_keys a LEFT JOIN `+view.source+` f(v,w) ON a.k=1`)
		}
		// All three frozen views retain TEXT alias authority, including both
		// zero producer rows and an emitted NULL cell. Future rows distinguish
		// the present source from null-extension without cell-based presence.
		checkViews := func(backend fixture.Backend, executor fixture.Executor, future bool) {
			names := []string{"k", "bare", "scalar_star", "constructed", "null_count", "qualified"}
			types := []string{"INT4", "TEXT", "TEXT", "TEXT", "INT4", "TEXT"}
			present := [][]any{
				{int64(1), "a", "a", "(a)", int64(0), "a"},
				{int64(1), "b", "b", "(b)", int64(0), "b"},
			}
			nulls := [][]any{{int64(1), nil, nil, "()", int64(1), nil}}
			if future {
				absent := []any{int64(2), nil, nil, "()", int64(1), nil}
				present = append(present, absent)
				nulls = append(nulls, absent)
			}
			compositeExprBoth(t, p, backend, executor,
				`SELECT k,bare,scalar_star,constructed,null_count,qualified FROM function_alias_present ORDER BY k,qualified`,
				names, types, present)
			for _, view := range []string{"function_alias_absent", "function_alias_null"} {
				compositeExprBoth(t, p, backend, executor,
					`SELECT k,bare,scalar_star,constructed,null_count,qualified FROM `+view+` ORDER BY k`,
					names, types, nulls)
			}
			for _, view := range []struct {
				name  string
				whole any
				nulls int64
			}{
				{"function_tuple_absent", nil, 1},
				{"function_tuple_null", "(,)", 0},
			} {
				want := [][]any{{int64(1), view.whole, view.nulls}}
				if future {
					want = append(want, []any{int64(2), nil, int64(1)})
				}
				compositeExprBoth(t, p, backend, executor,
					`SELECT k,whole,null_count FROM `+view.name+` ORDER BY k`,
					[]string{"k", "whole", "null_count"}, []string{"INT4", "TEXT", "INT4"}, want)
			}
		}
		futureRows := func(backend fixture.Backend, executor fixture.Executor) {
			step := fixture.Exec(`INSERT INTO function_alias_keys VALUES(2)`, 1)
			step.Prepared = true
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
			checkViews(backend, executor, true)
		}
		checkViews(b, c, false)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				checkViews(b, c, false)
				futureRows(b, c)
			}
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual function-alias SQL replay", err, dump.String())
				}
				pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
				checkViews(pg, p.Postgres, false)
				futureRows(pg, p.Postgres)
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			futureRows(b, c)
		}
	})
}
