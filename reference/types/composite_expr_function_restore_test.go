package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// SQL expectations run independently on both backends. Native controls additionally
// exercise actual replacement, independent destination ownership and emitted SQL;
// PostgreSQL replay is not a substitute for its independent SQL/view oracle.
func TestReferenceCompositeExprFunctionSourceRestore(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE function_restore_input(k integer,s text)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO function_restore_input VALUES(1,'ab'),(2,'z'),(3,NULL)`, 3))
		for _, view := range []struct{ name, source string }{
			{"function_restore_number", `regexp_count(a.s,'b')`},
			{"function_restore_bool", `regexp_like(a.s,'b')`},
			{"function_restore_array", `regexp_match(a.s,'(a)(b)')`},
			{"function_restore_text", `regexp_split_to_table(a.s,',')`},
		} {
			fixture.CompositeReferenceSetup(t, p, b, c,
				`CREATE VIEW `+view.name+` AS SELECT a.k,f AS bare,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count,f.v AS qualified,(SELECT f.*::text) AS outer_value FROM function_restore_input a LEFT JOIN `+view.source+` f(v) ON TRUE`)
		}
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE VIEW function_restore_top AS SELECT a.k,f.* FROM function_restore_input a LEFT JOIN regexp_count(a.s,'b') f(v) ON TRUE`,
			`CREATE VIEW function_restore_ordinal AS SELECT a.k,f::text AS whole,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count,f.n AS ordinal,f.v AS matched,(SELECT f.*::text) AS outer_value FROM function_restore_input a LEFT JOIN regexp_matches(a.s,'(a)(b)','g') WITH ORDINALITY f(v,n) ON TRUE`,
			`CREATE VIEW function_restore_tuple_null AS SELECT a.k,f::text AS whole,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count,f.v AS first,f.w AS second,(SELECT f.*::text) AS outer_value FROM function_restore_input a LEFT JOIN ROWS FROM(regexp_match(a.s,'(a)'),regexp_match(a.s,'(b)')) f(v,w) ON TRUE`,
			`CREATE VIEW function_restore_tuple_absent AS SELECT a.k,f::text AS whole,f.*::text AS scalar_star,ROW(f.*)::text AS constructed,num_nulls(f) AS null_count,f.v AS first,f.w AS second,(SELECT f.*::text) AS outer_value FROM function_restore_input a LEFT JOIN ROWS FROM(regexp_matches(a.s,'(a)','g'),regexp_matches(a.s,'(b)','g')) f(v,w) ON TRUE`)

		check := func(backend fixture.Backend, executor fixture.Executor, future bool) {
			// A declared alias is INT4, BOOL, TEXT[] or TEXT, never a one-field
			// RECORD. ROW(f.*) alone introduces a record, even for NULL cells.
			for _, sample := range []struct {
				view, typ string
				rows      [][]any
			}{
				{"function_restore_number", "INT4", [][]any{
					{int64(1), int64(1), "1", "(1)", int64(0), int64(1), "1"},
					{int64(2), int64(0), "0", "(0)", int64(0), int64(0), "0"},
					{int64(3), nil, nil, "()", int64(1), nil, nil},
				}},
				{"function_restore_bool", "BOOL", [][]any{
					{int64(1), true, "true", "(t)", int64(0), true, "true"},
					{int64(2), false, "false", "(f)", int64(0), false, "false"},
					{int64(3), nil, nil, "()", int64(1), nil, nil},
				}},
				{"function_restore_array", "_TEXT", [][]any{
					{int64(1), "{a,b}", "{a,b}", `("{a,b}")`, int64(0), "{a,b}", "{a,b}"},
					{int64(2), nil, nil, "()", int64(1), nil, nil},
					{int64(3), nil, nil, "()", int64(1), nil, nil},
				}},
				{"function_restore_text", "TEXT", [][]any{
					{int64(1), "ab", "ab", "(ab)", int64(0), "ab", "ab"},
					{int64(2), "z", "z", "(z)", int64(0), "z", "z"},
					{int64(3), nil, nil, "()", int64(1), nil, nil},
				}},
			} {
				if future {
					switch sample.view {
					case "function_restore_number":
						sample.rows[0] = []any{int64(1), int64(2), "2", "(2)", int64(0), int64(2), "2"}
						sample.rows = append(sample.rows, []any{int64(4), int64(1), "1", "(1)", int64(0), int64(1), "1"})
					case "function_restore_text":
						sample.rows[0] = []any{int64(1), "abab", "abab", "(abab)", int64(0), "abab", "abab"}
						sample.rows = append(sample.rows, []any{int64(4), "ab", "ab", "(ab)", int64(0), "ab", "ab"})
					default:
						row := append([]any(nil), sample.rows[0]...)
						row[0] = int64(4)
						sample.rows = append(sample.rows, row)
					}
					row := append([]any(nil), sample.rows[1]...)
					row[0] = int64(5)
					sample.rows = append(sample.rows, row)
				}
				compositeExprBoth(t, p, backend, executor,
					`SELECT k,bare,scalar_star,constructed,null_count,qualified,outer_value FROM `+sample.view+` ORDER BY k`,
					[]string{"k", "bare", "scalar_star", "constructed", "null_count", "qualified", "outer_value"},
					[]string{"INT4", sample.typ, "TEXT", "TEXT", "INT4", sample.typ, "TEXT"}, sample.rows)
			}
			top := [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}, {int64(3), nil}}
			if future {
				top[0][1] = int64(2)
				top = append(top, []any{int64(4), int64(1)}, []any{int64(5), int64(0)})
			}
			compositeExprBoth(t, p, backend, executor, `SELECT k,v FROM function_restore_top ORDER BY k`, []string{"k", "v"}, []string{"INT4", "INT4"}, top)
			ordinal := [][]any{
				{int64(1), `("{a,b}",1)`, `("{a,b}",1)`, `("{a,b}",1)`, int64(0), int64(1), "{a,b}", `("{a,b}",1)`},
				{int64(2), nil, nil, "(,)", int64(1), nil, nil, nil},
				{int64(3), nil, nil, "(,)", int64(1), nil, nil, nil},
			}
			if future {
				ordinal = append(ordinal[:1], append([][]any{
					{int64(1), `("{a,b}",2)`, `("{a,b}",2)`, `("{a,b}",2)`, int64(0), int64(2), "{a,b}", `("{a,b}",2)`},
				}, ordinal[1:]...)...)
				ordinal = append(ordinal, []any{int64(4), `("{a,b}",1)`, `("{a,b}",1)`, `("{a,b}",1)`, int64(0), int64(1), "{a,b}", `("{a,b}",1)`})
				ordinal = append(ordinal, []any{int64(5), nil, nil, "(,)", int64(1), nil, nil, nil})
			}
			compositeExprBoth(t, p, backend, executor,
				`SELECT k,whole,scalar_star,constructed,null_count,ordinal,matched,outer_value FROM function_restore_ordinal ORDER BY k,ordinal`,
				[]string{"k", "whole", "scalar_star", "constructed", "null_count", "ordinal", "matched", "outer_value"},
				[]string{"INT4", "TEXT", "TEXT", "TEXT", "INT4", "INT8", "_TEXT", "TEXT"}, ordinal)
			for _, sample := range []struct {
				view  string
				whole any
				nulls int64
			}{
				{"function_restore_tuple_null", "(,)", 0},
				{"function_restore_tuple_absent", nil, 1},
			} {
				rows := [][]any{
					{int64(1), "({a},{b})", "({a},{b})", "({a},{b})", int64(0), "{a}", "{b}", "({a},{b})"},
					{int64(2), sample.whole, sample.whole, "(,)", sample.nulls, nil, nil, sample.whole},
					{int64(3), sample.whole, sample.whole, "(,)", sample.nulls, nil, nil, sample.whole},
				}
				if future {
					if sample.view == "function_restore_tuple_absent" {
						rows = append(rows[:1], append([][]any{{int64(1), "({a},{b})", "({a},{b})", "({a},{b})", int64(0), "{a}", "{b}", "({a},{b})"}}, rows[1:]...)...)
					}
					rows = append(rows, []any{int64(4), "({a},{b})", "({a},{b})", "({a},{b})", int64(0), "{a}", "{b}", "({a},{b})"})
					rows = append(rows, []any{int64(5), sample.whole, sample.whole, "(,)", sample.nulls, nil, nil, sample.whole})
				}
				compositeExprBoth(t, p, backend, executor,
					`SELECT k,whole,scalar_star,constructed,null_count,first,second,outer_value FROM `+sample.view+` ORDER BY k`,
					[]string{"k", "whole", "scalar_star", "constructed", "null_count", "first", "second", "outer_value"},
					[]string{"INT4", "TEXT", "TEXT", "TEXT", "INT4", "_TEXT", "_TEXT", "TEXT"}, rows)
			}
		}
		futureWrites := func(backend fixture.Backend, executor fixture.Executor) {
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Prepared(fixture.Exec(`UPDATE function_restore_input SET s=$1 WHERE k=1`, 1, "abab")))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO function_restore_input VALUES($1,$2),($3,$4)`, 2, 4, "ab", 5, "z")))
			check(backend, executor, true)
		}
		check(b, c, false)
		if b.PG {
			futureWrites(b, c)
			return
		}

		// Keep the original public eager reader, not a reissued query, across
		// two replacements and changed producer data. Its descriptor stays owned.
		held, err := b.DB.QueryContext(p.Ctx, `SELECT k,v FROM function_restore_top ORDER BY k`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
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
			check(b, c, false)
			futureWrites(b, c)
		}
		fixture.UpdateCoreRetained(t, false, held, []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}, {int64(3), nil}}, false)

		// A fresh public Driver has no producer catalog or retained source plan.
		// Its genuine native fixture retains middleware recording for reads,
		// while unwrapped controls share that independent driver's connector.
		destination := fixture.NewFixture(t, false, true)
		target := destination.Backends()[0]
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, destination.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(target, destination.Memory, false)
			futureWrites(target, destination.Memory)
		}

		if p.Postgres != nil {
			clear := func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
					t.Fatal(err)
				}
			}
			clear()
			// This is the actual native-emitted script, not reconstructed SQL.
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual regexp function-source SQL replay", err, dump.String())
			}
			pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
			check(pg, p.Postgres, false)
			futureWrites(pg, p.Postgres)
			clear()
		}
	})
}
