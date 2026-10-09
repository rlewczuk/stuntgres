package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceQueryAliasesResolutionSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stringCoreControlExec(t, backend.db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					stringCoreControlExec(t, backend.db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
				}
				for _, tc := range queryAliasesResolutionCases() {
					want := tc.want
					if population == "empty" {
						want = [][]any{}
					}
					step := referenceSelect(tc.query, tc.names, tc.types, want)
					step.nonContext = true
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				}
			}
		})
	}
}

func TestReferenceQueryAliasesDiagnosticsSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stringCoreControlExec(t, backend.db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			stringCoreControlExec(t, backend.db, "CREATE TABLE u(id INT)")
			for _, population := range []string{"empty", "populated"} {
				if population == "populated" {
					stringCoreControlExec(t, backend.db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
					stringCoreControlExec(t, backend.db, "INSERT INTO u VALUES(1)")
				}
				for _, tc := range queryAliasesDiagnosticCases() {
					step := referenceImplicitError(tc.query, tc.want)
					step.nonContext, step.selectRows, step.errorStage = true, true, "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					step.errorStage = "prepare"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				}
			}
		})
	}
}

func TestReferenceQueryAliasesMetadataSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			stringCoreControlExec(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
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
				stringCoreRetainedRows(t, rows, err, []string{"sum"}, []string{"INT4"}, tc.want, false)
			}
			if !backend.pg {
				ctx, cancel := context.WithCancel(p.ctx)
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
				if backend.pg {
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
					if backend.pg {
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
			stringCoreControlExec(t, db, "CREATE TABLE typed(s VARCHAR(5),n NUMERIC(7,2))")
			stringCoreControlExec(t, db, "INSERT INTO typed VALUES('hello',12.50)")
			for _, empty := range []bool{false, true} {
				q := "SELECT x.word AS label,x.amount AS price FROM typed x(word,amount)"
				want := [][]any{{"hello", "12.50"}}
				if empty {
					q += " WHERE FALSE"
					want = [][]any{}
				}
				step := referenceSelect(q, []string{"label", "price"}, []string{"VARCHAR", "NUMERIC"}, want)
				step.nonContext = true
				step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
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
				referenceRunStep(t, p.ctx, db, backend.recorder, step)
				referenceRunStep(t, p.ctx, db, backend.recorder, referencePrepared(step))
			}
		})
	}
}

func TestReferenceQueryAliasesFailureSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(id INT NOT NULL,a INT,b INT)")
			stringCoreControlExec(t, db, "INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)")
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
					if !backend.pg {
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
				stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			}
			ctx, cancel := context.WithCancel(p.ctx)
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
