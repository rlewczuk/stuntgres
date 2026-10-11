package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

type integerRangeFailingValuer struct{ err error }

func (v integerRangeFailingValuer) Value() (driver.Value, error) { return nil, v.err }

func TestReferenceIntegerRangeBinds(t *testing.T) {
	for _, typ := range []string{"int4range", "int8range"} {
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
				bytes := []byte("[1,4]")
				var nilBytes []byte
				inputs := []any{"[1,4]", "(1,4]", bytes, nil, nilBytes, "empty", "[,]"}
				outputs := []any{"[1,5)", "[2,5)", "[1,5)", nil, nil, "empty", "(,)"}
				want := make([][]any, 0, len(inputs))
				for i, input := range inputs {
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, 1, i, input))
					want = append(want, []any{int64(i), outputs[i], outputs[i]})
					if i == 2 {
						for j := range bytes {
							bytes[j] = 'x'
						}
					}
				}
				// Direct execution shares the same text importer, including typed nil.
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO r VALUES ($1,$2,$3)", 1, 20, []byte("(1,4)"), nilBytes))
				want = append(want, []any{int64(20), "[2,4)", nil})
				valuerError := errors.New("range argument conversion failed")
				for _, execute := range []func() (sql.Result, error){
					func() (sql.Result, error) {
						return backend.DB.ExecContext(p.Ctx, query, 99, integerRangeFailingValuer{valuerError})
					},
					func() (sql.Result, error) { return stmt.ExecContext(p.Ctx, 99, integerRangeFailingValuer{valuerError}) },
				} {
					result, err := execute()
					var state interface{ SQLState() string }
					if result != nil || !errors.Is(err, valuerError) || errors.As(err, &state) {
						t.Fatal("Valuer error crossed SQL boundary", result, err)
					}
				}
				name := "INT4RANGE"
				if typ == "int8range" {
					name = "INT8RANGE"
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprint(prepared), func(t *testing.T) {
						s := integerRangeSelect(backend, "SELECT k,a,b FROM r ORDER BY k", []string{"k", "a", "b"}, []string{"INT4", name, name}, want, []bool{true, true, true})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					})
				}
			}
		})
	}
}
