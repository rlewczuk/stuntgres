package reference_test

import "testing"

func TestReferenceMathScalarHeldParameterContracts(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				query, name, typ, bad, message string
				good, want                     any
			}{
				{"SELECT factorial($1)", "factorial", "NUMERIC", "1.5", `invalid input syntax for type bigint: "1.5"`, int64(5), "120"},
				{"SELECT exp($1)", "exp", "FLOAT8", "oops", `invalid input syntax for type double precision: "oops"`, "0", float64(1)},
				{"SELECT div($1,$2)", "div", "NUMERIC", "oops", `invalid input syntax for type numeric: "oops"`, "9.00", "4"},
			} {
				func() {
					stmt, err := backend.db.PrepareContext(p.ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := stmt.Close(); err != nil {
							t.Error(err)
						}
					}()
					held := referenceHeldStatement{backend.db, stmt, tc.query}
					args := []any{tc.bad}
					if tc.name == "div" {
						args = append(args, "2")
					}
					failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22P02", Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, args...)
					failure.errorStage = "execute"
					referenceRunStep(t, p.ctx, referenceArithmeticExecutor{held, t}, backend.recorder, failure)
					for _, input := range []any{tc.good, nil, tc.good} {
						args[0] = input
						want := tc.want
						if input == nil {
							want = nil
						}
						referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(tc.query, []string{tc.name}, []string{tc.typ}, [][]any{{want}}, args...))
					}
				}()
			}
		})
	}
	p.step(t, referenceExec("CREATE TABLE names(abs SMALLINT,ceil REAL,ceiling NUMERIC,floor INT,factorial BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO names VALUES(-5,-1.5,-1.50,-1,5)", 1))
	step := referenceSelect(`SELECT floor,"abs"(abs),ceil(ceil),ceiling(ceiling),factorial(abs(abs)),factorial(factorial) FROM names`, []string{"floor", "abs", "ceil", "ceiling", "factorial", "factorial"}, []string{"INT4", "INT2", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{int64(-1), int64(5), float64(-1), "-1", "120", "120"}})
	p.step(t, step)
	p.step(t, referencePrepared(step))
}

func TestReferenceMathScalarHeldFrames(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			const query = "SELECT div($1,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			}()
			held := referenceHeldStatement{backend.db, stmt, query}
			for _, tc := range []struct{ a, b, want any }{{"9007199254740993", "2", "4503599627370496"}, {nil, "2", nil}, {"-9.00", "4.000", "-2"}, {"9", "0", nil}, {"9", "4", "2"}} {
				if tc.b == "0" {
					failure := referenceImplicitError(query, referenceDiagnostic{Code: "22012", Message: "division by zero"}, tc.a, tc.b)
					failure.selectRows, failure.errorStage = true, "execute"
					referenceRunStep(t, p.ctx, held, backend.recorder, failure)
				} else {
					referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(query, []string{"div"}, []string{"NUMERIC"}, [][]any{{tc.want}}, tc.a, tc.b))
				}
			}
		})
	}
	for _, tc := range []struct {
		query, name, typ string
		arg, want        any
	}{
		{"SELECT exp($1)", "exp", "FLOAT8", "0", float64(1)},
		{"SELECT factorial($1)", "factorial", "NUMERIC", "20", "2432902008176640000"},
		{"SELECT abs($1)", "abs", "FLOAT8", "-17.5", float64(17.5)},
	} {
		p.step(t, referencePrepared(referenceSelect(tc.query, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}}, tc.arg)))
	}
}
