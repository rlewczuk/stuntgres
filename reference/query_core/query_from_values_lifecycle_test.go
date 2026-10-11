package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryFromValuesPrepared(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		q := "SELECT n FROM (VALUES($1),(2)) v(n) ORDER BY n"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := fixture.HeldStatement{backend.DB, stmt, q}
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{int64(1), [][]any{{int64(1)}, {int64(2)}}},
			{int64(3), [][]any{{int64(2)}, {int64(3)}}},
			{nil, [][]any{{int64(2)}, {nil}}},
		} {
			fixture.RunStep(t, p.Ctx, x, backend.Recorder, fixture.Select(q, []string{"n"}, []string{"INT4"}, tc.rows, tc.arg))
		}
		q = "VALUES($1),($2)"
		unknown, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer unknown.Close()
		y := fixture.HeldStatement{backend.DB, unknown, q}
		for _, args := range [][]any{{"a", "b"}, {nil, "x"}, {"z", nil}} {
			s := fixture.Select(q, []string{"column1"}, []string{"TEXT"}, [][]any{{args[0]}, {args[1]}}, args...)
			s.Bag = true
			fixture.RunStep(t, p.Ctx, y, backend.Recorder, s)
		}
	}
}

func TestReferenceQueryFromValuesParameters(t *testing.T) {
	for _, tc := range []struct {
		query           string
		columns, types  []string
		args            [][]any
		rows            [][][]any
		missingPosition string
	}{
		{"VALUES($1),(2)", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}, [][][]any{{{int64(1)}, {int64(2)}}, {{nil}, {int64(2)}}}, "8"},
		{"VALUES($1),($2)", []string{"column1"}, []string{"TEXT"}, [][]any{{"a", "b"}, {nil, nil}}, [][][]any{{{"a"}, {"b"}}, {{nil}, {nil}}}, "8"},
		{"SELECT n FROM (VALUES($1),(2)) v(n) WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}, [][][]any{{}, {}}, "23"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				// Preserve the existing public-driver protocol boundary: pq's
				// no-argument call uses simple protocol, native binds then checks
				// Go argument arity. A held statement checks Go arity on both.
				rows, err := backend.DB.QueryContext(p.Ctx, tc.query)
				if rows != nil {
					rows.Close()
					t.Fatal("missing arguments published rows")
				}
				if backend.PG {
					want := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
					want.Position = tc.missingPosition
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					if err == nil {
						t.Fatal("missing Go arguments accepted")
					}
					if _, ok := err.(interface{ SQLState() string }); ok {
						t.Fatal("ordinary argument error gained SQLSTATE", err)
					}
				}
				stmt, err := backend.DB.PrepareContext(p.Ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{backend.DB, stmt, tc.query}
				for i, args := range tc.args {
					for _, executor := range []fixture.Executor{backend.DB, held} {
						s := fixture.Select(tc.query, tc.columns, tc.types, tc.rows[i], args...)
						s.Bag = true
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
					}
				}
				rows, err = stmt.QueryContext(p.Ctx)
				if rows != nil {
					rows.Close()
					stmt.Close()
					t.Fatal("missing held arguments published rows")
				}
				if err == nil {
					stmt.Close()
					t.Fatal("missing held Go arguments accepted")
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					stmt.Close()
					t.Fatal("ordinary held argument error gained SQLSTATE", err)
				}
				if tc.query == "VALUES($1),(2)" {
					for _, executor := range []fixture.Executor{backend.DB, held} {
						s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
					}
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestReferenceQueryFromValuesParameterDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		query          string
		args           []any
		want           fixture.Diagnostic
		missingMessage string
	}{
		{"VALUES($2),(2)", []any{nil, int64(1)}, fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}, "there is no parameter $2"},
		{"VALUES($1,$1),(1,TRUE)", []any{nil}, fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "11"}, "there is no parameter $1"},
		{"VALUES($1::integer),($1::boolean)", []any{nil}, fixture.Diagnostic{Code: "42804", Message: "VALUES types integer and boolean cannot be matched", Position: "22"}, "there is no parameter $1"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				plain := tc.want
				if backend.PG {
					plain = fixture.Diagnostic{Code: "42P02", Message: tc.missingMessage, Position: "8"}
				}
				s := fixture.ImplicitError(tc.query, plain)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, tc.want, tc.args...)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
