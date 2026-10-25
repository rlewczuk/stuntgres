package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMultirangeBinds(t *testing.T) {
	for _, typ := range []string{"int4multirange", "int8multirange", "nummultirange"} {
		t.Run(typ, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE r(k integer,a "+typ+",b "+typ+")", 0))
				query := "INSERT INTO r VALUES ($1,$2,$2)"
				stmt, err := backend.DB.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: query}
				owned := []byte("{[1,3),[2,4),empty,[8,9)}")
				inputs := []any{"{[1,3),[2,4)}", "{[4,6)}", owned, nil, []byte(nil), "{}", "{(,)}"}
				outputs := []any{"{[1,4)}", "{[4,6)}", "{[1,4),[8,9)}", nil, nil, "{}", "{(,)}"}
				want := make([][]any, 0, len(inputs)+1)
				for k, input := range inputs {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 1, k, input))
					want = append(want, []any{int64(k), outputs[k], outputs[k]})
					if k == 2 {
						check := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", strings.ToUpper(typ), strings.ToUpper(typ)}, want, []bool{true, true, true})
						rows, err := backend.DB.QueryContext(p.Ctx, check.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						check.CheckMetadata(t, rows)
						for i := range owned {
							owned[i] = 'x'
						}
						fixture.RangeConsumeHeld(t, rows, check)
					}
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 20, []byte("{[1,2),[3,4)}"), []byte(nil)))
				want = append(want, []any{int64(20), "{[1,2),[3,4)}", nil})
				failure := errors.New("multirange argument conversion failed")
				for _, execute := range []func() (sql.Result, error){
					func() (sql.Result, error) {
						return backend.DB.ExecContext(p.Ctx, query, 99, integerRangeFailingValuer{failure})
					},
					func() (sql.Result, error) { return stmt.ExecContext(p.Ctx, 99, integerRangeFailingValuer{failure}) },
				} {
					result, err := execute()
					var state interface{ SQLState() string }
					if result != nil || !errors.Is(err, failure) || errors.As(err, &state) {
						t.Fatal("Valuer error crossed SQL boundary", result, err)
					}
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := stmt.ExecContext(ctx, 99, "{}")
				var state interface{ SQLState() string }
				if result != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("cancellation crossed SQL boundary", result, err)
				}
				diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `malformed multirange literal: ""`, Detail: "Missing left brace.", Context: `unnamed portal parameter $1 = ''`}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO r VALUES (99,$1,$1)", diagnostic, []byte{})))
				for _, prepared := range []bool{false, true} {
					s := rangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", strings.ToUpper(typ), strings.ToUpper(typ)}, want, []bool{true, true, true})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
