package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAliasesResolutionSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.StringCoreControlExec(t, backend.DB, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					fixture.StringCoreControlExec(t, backend.DB, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
				}
				for _, tc := range queryAliasesResolutionCases() {
					want := tc.want
					if population == "empty" {
						want = [][]any{}
					}
					step := fixture.Select(tc.query, tc.names, tc.types, want)
					step.NonContext = true
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				}
			}
		})
	}
}

func TestReferenceQueryAliasesDiagnosticsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.StringCoreControlExec(t, backend.DB, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			fixture.StringCoreControlExec(t, backend.DB, "CREATE TABLE u(id INT)")
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					fixture.StringCoreControlExec(t, backend.DB, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
					fixture.StringCoreControlExec(t, backend.DB, "INSERT INTO u VALUES(1)")
				}
				for _, tc := range queryAliasesDiagnosticCases() {
					step := fixture.ImplicitError(tc.query, tc.want)
					step.NonContext, step.SelectRows, step.ErrorStage = true, true, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					step.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				}
			}
		})
	}
}

func TestReferenceQueryAliasesMetadataSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
			stmt, err := db.Prepare("SELECT x.iid+$1 AS sum FROM t x(iid) WHERE x.iid=$2 ORDER BY sum")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				args []any
				want [][]any
			}{
				{[]any{10, 1}, [][]any{{int64(11)}}}, {[]any{20, 2}, [][]any{{int64(22)}}},
				{[]any{nil, 2}, [][]any{{nil}}}, {[]any{10, nil}, [][]any{}},
			} {
				rows, err := stmt.Query(tc.args...)
				fixture.StringCoreRetainedRows(t, rows, err, []string{"sum"}, []string{"INT4"}, tc.want, false)
			}
			if !backend.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := stmt.QueryContext(ctx, 10, 1)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled parameterized held execution exposed rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("context failure must remain ordinary", err)
				}
			}
			for _, args := range [][]any{{10}, {struct{}{}, 1}} {
				rows, err := stmt.Query(args...)
				if rows != nil {
					rows.Close()
					t.Fatal("invalid arguments exposed rows")
				}
				var state interface{ SQLState() string }
				if err == nil || errors.As(err, &state) {
					t.Fatal("argument failure must be ordinary", err)
				}
			}
			for _, q := range []string{"SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid)", "SELECT x.iid AS shown,x.a,x.iid+1 AS computed FROM t x(iid) WHERE FALSE"} {
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"shown", "a", "computed"}) {
					t.Fatal(names, err)
				}
				names[0] = "mutated"
				wantNames := []string{"shown", "a", "computed"}
				if backend.PG {
					wantNames[0] = "mutated"
				}
				next, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(next, wantNames) {
					t.Fatal(next, err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 3 {
					t.Fatal(metadata, err)
				}
				for i, typ := range metadata {
					nullable, known := typ.Nullable()
					wantNullable, wantKnown := i == 1, i < 2
					if backend.PG {
						wantNullable, wantKnown = false, false
					}
					if typ.Name() != wantNames[i] || nullable != wantNullable || known != wantKnown {
						t.Fatal("alias descriptor", i, typ, nullable, known)
					}
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			fixture.StringCoreControlExec(t, db, "CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))")
			fixture.StringCoreControlExec(t, db, "INSERT INTO typed VALUES('hello',12.50)")
			for _, empty := range []bool{false, true} {
				q := "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)"
				want := [][]any{{"hello", "12.50"}}
				if empty {
					q += " WHERE FALSE"
					want = [][]any{}
				}
				step := fixture.Select(q, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want)
				step.NonContext = true
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != 2 {
						t.Fatal(metadata, err)
					}
					if length, ok := metadata[0].Length(); !ok || length != 5 {
						t.Fatal("VARCHAR length", length, ok)
					}
					if precision, scale, ok := metadata[1].DecimalSize(); !ok || precision != 7 || scale != 2 {
						t.Fatal("NUMERIC typmod", precision, scale, ok)
					}
				}
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, step)
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Prepared(step))
			}
		})
	}
}

func TestReferenceQueryAliasesFailureSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
			q := "SELECT 1/(x.iid-2) AS quotient FROM t x(iid) ORDER BY x.iid"
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.Query()
				} else {
					rows, err = db.Query(q)
				}
				if rows != nil {
					if !backend.PG {
						rows.Close()
						t.Fatal("native failure exposed partial cursor")
					}
					if rows.Next() {
						rows.Close()
						t.Fatal("division failure exposed partial row")
					}
					err = rows.Err()
					if closeErr := rows.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
				}
				fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = db.QueryContext(ctx, q)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query exposed rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("ordinary cancellation", err)
				}
			}
		})
	}
}
