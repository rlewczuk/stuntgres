package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

type queryAliasesResolutionCase struct {
	query        string
	names, types []string
	want         [][]any
}

func queryAliasesResolutionCases() []queryAliasesResolutionCase {
	all := [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(5)}, {int64(3), nil, int64(2)}, {int64(4), int64(2), int64(0)}}
	return []queryAliasesResolutionCase{
		{"SELECT x.iid AS shown,x.a,x.b FROM t AS x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.iid AS shown,x.a,x.b FROM t x(iid) ORDER BY shown", []string{"shown", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT x.* FROM t x(iid) ORDER BY x.iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{"SELECT iid,a,b FROM t x(iid) ORDER BY iid", []string{"iid", "a", "b"}, []string{"INT4", "INT4", "INT4"}, all},
		{`SELECT "Mixed"."Key" AS "Shown" FROM t AS "Mixed"("Key") ORDER BY "Shown"`, []string{"Shown"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
		{"SELECT x.iid,x.iid+1 FROM t x(iid) ORDER BY x.iid", []string{"iid", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}, {int64(4), int64(5)}}},
		{"SELECT a AS b,id FROM t WHERE b=1 ORDER BY b", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}},
		{"SELECT a AS b,id FROM t ORDER BY b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(4)}, {nil, int64(3)}}},
		{"SELECT a AS b,id FROM t ORDER BY t.b,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,id FROM t ORDER BY b+0,id", []string{"b", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}, {int64(2), int64(1)}, {nil, int64(3)}, {int64(1), int64(2)}}},
		{"SELECT a AS b,b FROM t ORDER BY id", []string{"b", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(5)}, {nil, int64(2)}, {int64(2), int64(0)}}},
		{"SELECT a AS x,a AS x FROM t ORDER BY x,id", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {nil, nil}}},
	}
}

func TestReferenceQueryAliasesResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	cases := queryAliasesResolutionCases()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
				}
				t.Run(population, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.query, func(t *testing.T) {
							want := tc.want
							if population == "empty" {
								want = [][]any{}
							}
							step := fixture.Select(tc.query, tc.names, tc.types, want)
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
						})
					}
				})
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE VIEW alias_view AS SELECT x.iid AS shown,x.a AS amount FROM t x(iid) WHERE x.b=1 ORDER BY shown", 0))
			step := fixture.Select("SELECT shown,amount FROM alias_view ORDER BY shown", []string{"shown", "amount"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
		})
	}
}

func TestReferenceQueryAliasesMetadataAndPreparedFreshness(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0),
				fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4),
				fixture.Exec("CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))", 0),
				fixture.Exec("INSERT INTO typed VALUES('hello',12.50)", 1),
			} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
			q := "SELECT x.iid+$1 AS sum FROM t x(iid) WHERE x.iid=$2 ORDER BY sum"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				args []any
				want [][]any
			}{
				{[]any{10, 1}, [][]any{{int64(11)}}},
				{[]any{20, 2}, [][]any{{int64(22)}}},
				{[]any{nil, 2}, [][]any{{nil}}},
				{[]any{10, nil}, [][]any{}},
			} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"sum"}, []string{"INT4"}, tc.want, tc.args...))
			}
			for _, args := range [][]any{{10}, {struct{}{}, 1}} {
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid arguments exposed rows")
				}
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("argument failure must be ordinary", err)
				}
			}
			for _, query := range []string{"SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid)", "SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid) WHERE FALSE"} {
				rows, err := backend.DB.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"shown", "a", "computed"}) {
					rows.Close()
					t.Fatal("initial alias descriptors", names, err)
				}
				// Native owns detached descriptors; pq exposes its column-name slice.
				wantNames := []string{"shown", "a", "computed"}
				if backend.PG {
					wantNames[0] = "mutated"
				}
				names[0] = "mutated"
				next, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(next, wantNames) {
					rows.Close()
					t.Fatal(next, err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 3 {
					rows.Close()
					t.Fatal(metadata, err)
				}
				for i, typ := range metadata {
					if typ.Name() != wantNames[i] {
						rows.Close()
						t.Fatal("alias metadata name", i, typ.Name(), wantNames[i])
					}
					nullable, known := typ.Nullable()
					wantNullable, wantKnown := i == 1, i < 2
					if backend.PG {
						wantNullable, wantKnown = false, false
					}
					if nullable != wantNullable || known != wantKnown {
						rows.Close()
						t.Fatal("alias nullability", i, nullable, known)
					}
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, empty := range []bool{false, true} {
				q := "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)"
				want := [][]any{{"hello", "12.50"}}
				if empty {
					q += " WHERE FALSE"
					want = [][]any{}
				}
				step := fixture.Select(q, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					var metadataStmt *sql.Stmt
					if prepared {
						metadataStmt, err = backend.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err = metadataStmt.QueryContext(p.Ctx)
					} else {
						rows, err = backend.DB.QueryContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := rows.ColumnTypes()
					rows.Close()
					if metadataStmt != nil {
						metadataStmt.Close()
					}
					if err != nil || len(metadata) != 2 {
						t.Fatal(metadata, err)
					}
					if length, ok := metadata[0].Length(); !ok || length != 5 {
						t.Fatal("alias lost VARCHAR length", length, ok)
					}
					if precision, scale, ok := metadata[1].DecimalSize(); !ok || precision != 7 || scale != 2 {
						t.Fatal("alias lost NUMERIC precision/scale", precision, scale, ok)
					}
				}
			}
		})
	}
}

func TestReferenceQueryAliasesFailure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
			q := "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("analysis must succeed before runtime division failure", err)
			}
			defer stmt.Close()
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = backend.DB.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query exposed rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("ordinary canceled query", err)
				}
			}
		})
	}
}
