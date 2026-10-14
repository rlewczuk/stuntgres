package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckMetadata(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.meta_int AS integer CHECK (VALUE>0)",
			"CREATE DOMAIN domain_s.meta_numeric AS numeric(5,2) CHECK (VALUE>0)",
			"CREATE DOMAIN domain_s.meta_varchar AS varchar(3) CHECK (VALUE<>'')",
			"CREATE TABLE domain_s.metadata (id integer,i domain_s.meta_int,n domain_s.meta_numeric,v domain_s.meta_varchar)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.metadata VALUES (1,1,12.345,'abc'),(2,NULL,NULL,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				s          fixture.Step
				expression bool
			}{
				{fixture.Select("SELECT i,n,v FROM domain_s.metadata ORDER BY id", []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, [][]any{{int64(1), "12.35", "abc"}, {nil, nil, nil}}), false},
				{fixture.Select("SELECT i,n,v FROM domain_s.metadata WHERE id=2", []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, [][]any{{nil, nil, nil}}), false},
				{fixture.Select("SELECT i,n,v FROM domain_s.metadata WHERE false", []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, [][]any{}), false},
				{fixture.Select("SELECT $1::domain_s.meta_int AS i,$2::domain_s.meta_numeric AS n,$3::domain_s.meta_varchar AS v FROM public.one", []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, [][]any{{int64(1), "12.35", "abc"}}, int64(1), "12.345", "abc"), true},
				{fixture.Select("SELECT $1::domain_s.meta_int AS i,$2::domain_s.meta_numeric AS n,$3::domain_s.meta_varchar AS v FROM public.one", []string{"i", "n", "v"}, []string{"INT4", "NUMERIC", "VARCHAR"}, [][]any{{nil, nil, nil}}, nil, nil, nil), true},
			} {
				t.Run(tc.s.Query+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
					s := tc.s
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						domainReferenceCheckMetadata(t, rows, b.PG, tc.expression, s.Types)
					}
					fixture.RunStep(t, p.Ctx, c, r, s)
				})
			}
		}
		const q = "SELECT $1::domain_s.meta_int AS v FROM public.one"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, value := range []any{int64(1), int64(0), int64(2), nil} {
			s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{value}}, value)
			if value == int64(0) {
				s = domainCheckFailure(q, "meta_int", "meta_int_check", true, false, value)
				s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
			}
			t.Run("held", func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, r, s) })
		}
	})
}
