package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDateRangeBinds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,a daterange,b daterange)", 0))
		query := "INSERT INTO r VALUES ($1,$2,$2)"
		stmt, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
		buf := []byte("[2000-02-28,2000-03-01]")
		var nilBytes []byte
		inputs := []any{"[2000-02-28,2000-03-01]", "(2000-02-28,2000-03-01]", buf, nil, nilBytes, "empty", "[,]", "[2000-01-01,infinity]"}
		outputs := []any{"[2000-02-28,2000-03-02)", "[2000-02-29,2000-03-02)", "[2000-02-28,2000-03-02)", nil, nil, "empty", "(,)", "[2000-01-01,infinity]"}
		want := make([][]any, 0, len(inputs)+1)
		for i, input := range inputs {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 1, i, input))
			want = append(want, []any{int64(i), outputs[i], outputs[i]})
			if i == 2 { for j := range buf { buf[j] = 'x' } }
		}
		run(fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 20, []byte("(2000-02-28,2000-03-01)"), nilBytes))
		want = append(want, []any{int64(20), "[2000-02-29,2000-03-01)", nil})
		conversion := errors.New("daterange argument conversion failed")
		for _, execute := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.DB.ExecContext(p.Ctx, query, 99, integerRangeFailingValuer{conversion}) },
			func() (sql.Result, error) { return stmt.ExecContext(p.Ctx, 99, integerRangeFailingValuer{conversion}) },
		} {
			result, err := execute()
			var state interface{ SQLState() string }
			if result != nil || !errors.Is(err, conversion) || errors.As(err, &state) { t.Fatal("Valuer error crossed SQL boundary", result, err) }
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, execute := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.DB.ExecContext(ctx, query, 99, "[epoch,epoch]") },
			func() (sql.Result, error) { return stmt.ExecContext(ctx, 99, "[epoch,epoch]") },
		} {
			result, err := execute()
			if result != nil || !errors.Is(err, context.Canceled) { t.Fatal("precanceled insert", result, err) }
		}
		for _, prepared := range []bool{false, true} {
			s := rangeSelect(b, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", "DATERANGE", "DATERANGE"}, want, []bool{false, true, true})
			s.Prepared = prepared
			run(s)
		}
	}
}
