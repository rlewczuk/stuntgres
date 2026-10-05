//go:build reference

package stuntgres_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func referenceTransactionRows(prepared bool, q string, values ...int64) referenceStep {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	return referenceStep{query: q, prepared: prepared, selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: rows}
}
func referenceTransactionError(prepared bool, q string, want referenceDiagnostic) referenceStep {
	return referenceStep{query: q, prepared: prepared, state: want.Code, diagnostic: &want}
}
func referenceTransactionAborted() referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
}
func referenceTransactionNull() referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
}
func referenceTransactionValues(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal("metadata", types, err)
	}
	// As in referenceRunStep, assert SQL types and actual returned values;
	// ScanType is adapter metadata, not a shared PostgreSQL type contract.
	transactionRowValues(t, rows, want...)
}
func TestReferenceTransactionVisibilityRefreshAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferencePair(t)
			var traces [2][]referenceObserved
			for index, db := range []*sql.DB{pair.memory, pair.postgres} {
				t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
					ctx := pair.ctx
					recorder := pair.memoryRecord
					if index == 1 {
						recorder = pair.postgresRecord
					}
					a, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					step := func(x referenceExecutor, s referenceStep) {
						traces[index] = append(traces[index], referenceRunStep(t, ctx, x, recorder, s))
					}
					insert := func(x referenceExecutor, q string, n int64) {
						step(x, referenceStep{query: q, prepared: prepared, affected: n})
					}
					step(b, referenceStep{query: "CREATE TABLE t(v INT)"})
					step(b, referenceStep{query: "CREATE TABLE u(v INT)"})
					for _, commit := range []bool{false, true} {
						tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						insert(tx, "INSERT INTO t VALUES(1)", 1)
						step(tx, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
						step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v"))
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if !errors.Is(tx.Commit(), sql.ErrTxDone) || !errors.Is(tx.Rollback(), sql.ErrTxDone) {
							t.Fatal("public transaction handle did not end")
						}
						if commit {
							step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
						} else {
							step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v"))
						}
					}
					reader, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					step(reader, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
					cached, err := reader.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					defer cached.Close()
					insert(a, "INSERT INTO t VALUES(3)", 1)
					rows, err := cached.QueryContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					referenceTransactionValues(t, rows, 1, 3)
					insert(reader, "INSERT INTO t VALUES(2)", 1)
					writer, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Rollback()
					insert(writer, "INSERT INTO t VALUES(4),(4)", 2)
					insert(writer, "INSERT INTO u VALUES(9)", 1)
					step(reader, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 3))
					step(reader, referenceTransactionRows(prepared, "SELECT v FROM u ORDER BY v"))
					if err = writer.Commit(); err != nil {
						t.Fatal(err)
					}
					step(reader, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4, 4))
					step(reader, referenceTransactionRows(prepared, "SELECT v FROM u ORDER BY v", 9))
					if err = reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					step(a, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 3, 4, 4))
				})
			}
			if !t.Failed() && !reflect.DeepEqual(traces[0], traces[1]) {
				t.Fatal("independently asserted transaction traces differ", traces)
			}
		})
	}
}
func TestReferenceTransactionFailuresAndAutocommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferencePair(t)
			var traces [2][]referenceObserved
			for index, db := range []*sql.DB{pair.memory, pair.postgres} {
				t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
					ctx := pair.ctx
					recorder := pair.memoryRecord
					if index == 1 {
						recorder = pair.postgresRecord
					}
					a, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					step := func(x referenceExecutor, s referenceStep) {
						traces[index] = append(traces[index], referenceRunStep(t, ctx, x, recorder, s))
					}
					insert := func(x referenceExecutor, q string, n int64) {
						step(x, referenceStep{query: q, prepared: prepared, affected: n})
					}
					step(b, referenceStep{query: "CREATE TABLE t(v INT NOT NULL)"})
					insert(b, "INSERT INTO t VALUES(7)", 1)
					if prepared {
						err = transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", true)
						want := referenceTransactionNull()
						referenceAssertDiagnostic(t, err, &want)
					} else {
						step(a, referenceTransactionError(false, "INSERT INTO t VALUES(8),(NULL)", referenceTransactionNull()))
					}
					step(a, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
					insert(a, "INSERT INTO t VALUES(9)", 1)
					for _, commit := range []bool{false, true} {
						tx, err := a.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						insert(tx, "INSERT INTO t VALUES(1)", 1)
						cached, err := tx.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
						if err != nil {
							t.Fatal(err)
						}
						if prepared {
							err = transactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(8),(NULL)", true)
							want := referenceTransactionNull()
							referenceAssertDiagnostic(t, err, &want)
						} else {
							step(tx, referenceTransactionError(false, "INSERT INTO t VALUES(8),(NULL)", referenceTransactionNull()))
						}
						step(tx, referenceTransactionError(false, "SELECT v FROM missing", referenceTransactionAborted()))
						_, err = cached.QueryContext(ctx)
						referenceAssertDiagnostic(t, err, func() *referenceDiagnostic { d := referenceTransactionAborted(); return &d }())
						cached.Close()
						step(tx, referenceTransactionError(true, "SELECT v FROM missing", referenceTransactionAborted()))
						step(tx, referenceTransactionError(false, "SELECT * FROM t WHERE v = )", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}))
						if commit {
							transactionOrdinary(t, tx.Commit(), "pq: could not complete operation in a failed transaction")
						} else if err = tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9))
					}
					syntaxTx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer syntaxTx.Rollback()
					insert(syntaxTx, "INSERT INTO t VALUES(1)", 1)
					syntax := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}
					for _, state := range []string{"active", "aborted"} {
						stmt, err := syntaxTx.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
						if stmt != nil {
							stmt.Close()
							t.Fatalf("%s malformed Prepare returned a statement", state)
						}
						referenceAssertDiagnostic(t, err, &syntax)
					}
					step(syntaxTx, referenceTransactionError(false, "INSERT INTO t VALUES(2)", referenceTransactionAborted()))
					if err = syntaxTx.Rollback(); err != nil {
						t.Fatal(err)
					}
					step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9))
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					insert(tx, "INSERT INTO t VALUES(1)", 1)
					missing := referenceTransactionError(true, "SELECT v FROM missing", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`})
					pgMissing := *missing.diagnostic
					pgMissing.Position = "15"
					missing.postgresDiagnostic = &pgMissing
					step(tx, missing)
					step(tx, referenceTransactionError(false, "INSERT INTO t VALUES(2)", referenceTransactionAborted()))
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					tx, err = a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					insert(tx, "INSERT INTO t VALUES(10)", 1)
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9, 10))
				})
			}
			if !t.Failed() && !reflect.DeepEqual(traces[0], traces[1]) {
				t.Fatal("independently asserted failure traces differ", traces)
			}
		})
	}
}
func TestReferenceTransactionCancellationOracle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferencePair(t)
			ctx := pair.ctx
			a, err := pair.postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := pair.postgres.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			referenceRunStep(t, ctx, b, pair.postgresRecord, referenceStep{query: "CREATE TABLE t(v INT)"})
			referenceRunStep(t, ctx, b, pair.postgresRecord, referenceStep{query: "INSERT INTO t VALUES(7)", affected: 1})
			var before, after int
			if err = a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, ctx, tx, pair.postgresRecord, referenceStep{query: "INSERT INTO t VALUES(1)", prepared: prepared, affected: 1})
			// pg_sleep is solely an oracle synchronization statement. Native cancellation
			// uses actual Operation contexts in TestTransactionPublicationLinearization.
			referenceSessionCancel(t, ctx, a, prepared)
			referenceRunStep(t, ctx, tx, pair.postgresRecord, referenceTransactionError(false, "SELECT v FROM t", referenceTransactionAborted()))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, ctx, b, pair.postgresRecord, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
			if err = a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&after); err != nil || after != before {
				t.Fatal("cancellation replaced healthy backend", before, after, err)
			}
			referenceRunStep(t, ctx, a, pair.postgresRecord, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
		})
	}
}
