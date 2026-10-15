package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeOperatorComposition(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE op(k integer,a int4range,b int4range,m int4multirange)", 0))
		run(fixture.Exec("INSERT INTO op VALUES (1,'[1,3)','[3,7)','{[1,3),[7,9)}'),(2,'[7,9)','[1,3)','{}'),(3,NULL,NULL,NULL)", 3))
		for _, tc := range []struct {
			expr string
			v    []any
		}{
			{"range_merge(a,a) -|- b", []any{true, false, nil}}, {"a -|- range_merge(b,b)", []any{true, false, nil}},
			{"range_merge(a,a) << b", []any{true, false, nil}}, {"a << range_merge(b,b)", []any{true, false, nil}},
			{"range_merge(a,a) >> b", []any{false, true, nil}}, {"a >> range_merge(b,b)", []any{false, true, nil}},
			{"range_merge(a,a) &< b", []any{true, false, nil}}, {"a &< range_merge(b,b)", []any{true, false, nil}},
			{"range_merge(a,a) &> b", []any{false, true, nil}}, {"a &> range_merge(b,b)", []any{false, true, nil}},
			{"a OPERATOR(pg_catalog.-|-) b", []any{true, false, nil}}, {"multirange(a) && m", []any{true, false, nil}},
			{"(a * b) = 'empty'", []any{true, true, nil}},
		} {
			rows := make([][]any, len(tc.v))
			for i, v := range tc.v {
				rows[i] = []any{v}
			}
			operatorBool(t, p, b, tc.expr, "op ORDER BY k", rows)
		}
		operatorBoth(t, p, b, fixture.Select("SELECT k FROM op WHERE a -|- b ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		operatorBoth(t, p, b, fixture.Select("SELECT x.k AS k FROM op AS x JOIN op AS y ON x.a && y.b WHERE y.k=1 ORDER BY x.k", []string{"k"}, []string{"INT4"}, [][]any{}))
		// Shared spellings must not displace existing array/numeric/bit routes.
		operatorBoth(t, p, b, fixture.Select("SELECT ARRAY[1,2] @> ARRAY[2] AS a,2 + 3 AS n,8 >> 1 AS s FROM op WHERE k=1", []string{"a", "n", "s"}, []string{"BOOL", "INT4", "INT4"}, [][]any{{true, int64(5), int64(4)}}))
	})
}

func TestReferenceRangeOperatorBinds(t *testing.T) {
	operatorMultirangeBinds(t)
	for _, f := range operatorFamilies {
		t.Run(f.name, func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE op(k integer,a "+f.name+")", 0))
				run(fixture.Exec("INSERT INTO op VALUES (1,"+rangeLiteral(f.span(f.one, f.three))+"),(2,NULL)", 2))
				q := "SELECT a @> $1 AS v FROM op ORDER BY k"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					arg any
					v   any
				}{{f.span(f.one, f.two), true}, {[]byte(f.span(f.one, f.two)), true}, {f.span(f.seven, f.nine), false}, {nil, nil}, {[]byte(nil), nil}} {
					s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.v}, {nil}}, tc.arg)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				// An actual open cursor owns bound byte input independently of its caller.
				owned := []byte(f.span(f.one, f.two))
				rows, err := stmt.QueryContext(p.Ctx, owned)
				if err != nil {
					t.Fatal(err)
				}
				for i := range owned {
					owned[i] = 'x'
				}
				got := []any{}
				for rows.Next() {
					var v any
					if err := rows.Scan(&v); err != nil {
						t.Fatal(err)
					}
					got = append(got, v)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if len(got) != 2 || got[0] != true || got[1] != nil {
					t.Fatalf("owned bind rows %#v", got)
				}
				operatorBool(t, p, b, "a @> $1", "op WHERE k=99", [][]any{}, nil)
				operatorBool(t, p, b, "a @> $1", "op WHERE k=2", [][]any{{nil}}, nil)
				operatorValue(t, p, b, "a + NULL", "op WHERE k=99", f.name, [][]any{})
				operatorValue(t, p, b, "a * NULL", "op WHERE k=2", f.name, [][]any{{nil}})
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, query := range []func() (*sql.Rows, error){func() (*sql.Rows, error) { return b.DB.QueryContext(canceled, q, nil) }, func() (*sql.Rows, error) { return stmt.QueryContext(canceled, nil) }} {
					rows, err := query()
					var state interface{ SQLState() string }
					if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
						t.Fatal("cancellation boundary", rows, err)
					}
				}
			})
		})
	}
}

func operatorMultirangeBinds(t *testing.T) {
	for _, f := range operatorFamilies[:3] {
		t.Run(f.multi(), func(t *testing.T) {
			rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE op(k integer,a "+f.multi()+")", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO op VALUES (1,'{[1,3),[7,9)}'),(2,NULL)", 2))
				q := "SELECT a @> $1 AS v,a - $1 AS d FROM op ORDER BY k"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				for _, tc := range []struct{ arg, v, d any }{
					{"{[1,2)}", true, "{[2,3),[7,9)}"}, {[]byte("{[1,2)}"), true, "{[2,3),[7,9)}"}, {"{[2,8)}", false, "{[1,2),[8,9)}"}, {"{}", true, "{[1,3),[7,9)}"}, {nil, nil, nil}, {[]byte(nil), nil, nil},
				} {
					s := rangeSelect(b, q, []string{"v", "d"}, []string{"BOOL", upperOperatorName(f.multi())}, [][]any{{tc.v, tc.d}, {nil, nil}}, nil)
					s.Args = []any{tc.arg}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				operatorValue(t, p, b, "a - NULL", "op WHERE k=99", f.multi(), [][]any{})
				operatorValue(t, p, b, "a - NULL", "op WHERE k=2", f.multi(), [][]any{{nil}})
			})
		})
	}
}
