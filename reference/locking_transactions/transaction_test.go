package reference_test

import (
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionVisibilityRefreshAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			var traces [2][]fixture.Observed
			for index, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := pair.Ctx
					db, recorder := backend.DB, backend.Recorder
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
					step := func(x fixture.Executor, s fixture.Step) {
						traces[index] = append(traces[index], fixture.RunStep(t, ctx, x, recorder, s))
					}
					insert := func(x fixture.Executor, q string, n int64) {
						step(x, fixture.Step{Query: q, Prepared: prepared, Affected: n})
					}
					step(b, fixture.Step{Query: "CREATE TABLE t(v INT)"})
					step(b, fixture.Step{Query: "CREATE TABLE u(v INT)"})
					for _, commit := range []bool{false, true} {
						tx, err := a.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						insert(tx, "INSERT INTO t VALUES(1)", 1)
						step(tx, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
						step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v"))
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
							step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
						} else {
							step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v"))
						}
					}
					reader, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Rollback()
					step(reader, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1))
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
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 3)
					insert(reader, "INSERT INTO t VALUES(2)", 1)
					writer, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Rollback()
					insert(writer, "INSERT INTO t VALUES(4),(4)", 2)
					insert(writer, "INSERT INTO u VALUES(9)", 1)
					step(reader, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 3))
					step(reader, fixture.TransactionRows(prepared, "SELECT v FROM u ORDER BY v"))
					if err = writer.Commit(); err != nil {
						t.Fatal(err)
					}
					step(reader, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4, 4))
					step(reader, fixture.TransactionRows(prepared, "SELECT v FROM u ORDER BY v", 9))
					if err = reader.Rollback(); err != nil {
						t.Fatal(err)
					}
					step(a, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 3, 4, 4))
				})
			}
			if pair.Postgres != nil && !t.Failed() && !reflect.DeepEqual(traces[0], traces[1]) {
				t.Fatal("independently asserted transaction traces differ", traces)
			}
		})
	}
}
func TestReferenceTransactionFailuresAndAutocommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			var traces [2][]fixture.Observed
			for index, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := pair.Ctx
					db, recorder := backend.DB, backend.Recorder
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
					step := func(x fixture.Executor, s fixture.Step) {
						traces[index] = append(traces[index], fixture.RunStep(t, ctx, x, recorder, s))
					}
					insert := func(x fixture.Executor, q string, n int64) {
						step(x, fixture.Step{Query: q, Prepared: prepared, Affected: n})
					}
					step(b, fixture.Step{Query: "CREATE TABLE t(v INT NOT NULL)"})
					insert(b, "INSERT INTO t VALUES(7)", 1)
					if prepared {
						err = fixture.TransactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(8),(NULL)", true)
						want := fixture.TransactionNull()
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						step(a, fixture.TransactionError(false, "INSERT INTO t VALUES(8),(NULL)", fixture.TransactionNull()))
					}
					step(a, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
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
							err = fixture.TransactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(8),(NULL)", true)
							want := fixture.TransactionNull()
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							step(tx, fixture.TransactionError(false, "INSERT INTO t VALUES(8),(NULL)", fixture.TransactionNull()))
						}
						step(tx, fixture.TransactionError(false, "SELECT v FROM missing", fixture.TransactionAborted()))
						_, err = cached.QueryContext(ctx)
						fixture.AssertDiagnostic(t, err, func() *fixture.Diagnostic { d := fixture.TransactionAborted(); return &d }())
						cached.Close()
						step(tx, fixture.TransactionError(true, "SELECT v FROM missing", fixture.TransactionAborted()))
						step(tx, fixture.TransactionError(false, "SELECT * FROM t WHERE v = )", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}))
						if commit {
							fixture.TransactionOrdinary(t, tx.Commit(), "pq: could not complete operation in a failed transaction")
						} else if err = tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9))
					}
					syntaxTx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer syntaxTx.Rollback()
					insert(syntaxTx, "INSERT INTO t VALUES(1)", 1)
					syntax := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "27"}
					for _, state := range []string{"active", "aborted"} {
						stmt, err := syntaxTx.PrepareContext(ctx, "SELECT * FROM t WHERE v = )")
						if stmt != nil {
							stmt.Close()
							t.Fatalf("%s malformed Prepare returned a statement", state)
						}
						fixture.AssertDiagnostic(t, err, &syntax)
					}
					step(syntaxTx, fixture.TransactionError(false, "INSERT INTO t VALUES(2)", fixture.TransactionAborted()))
					if err = syntaxTx.Rollback(); err != nil {
						t.Fatal(err)
					}
					step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9))
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					insert(tx, "INSERT INTO t VALUES(1)", 1)
					missing := fixture.TransactionError(true, "SELECT v FROM missing", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`, Position: "15"})
					step(tx, missing)
					step(tx, fixture.TransactionError(false, "INSERT INTO t VALUES(2)", fixture.TransactionAborted()))
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
					step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7, 9, 10))
				})
			}
			if pair.Postgres != nil && !t.Failed() && !reflect.DeepEqual(traces[0], traces[1]) {
				t.Fatal("independently asserted failure traces differ", traces)
			}
		})
	}
}
func referenceTransactionCancellationOracle(t *testing.T, pair *fixture.Pair, prepared bool) {
	t.Helper()
	ctx := pair.Ctx
	a, err := pair.Postgres.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := pair.Postgres.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	fixture.RunStep(t, ctx, b, pair.PostgresRecord, fixture.Step{Query: "CREATE TABLE t(v INT)"})
	fixture.RunStep(t, ctx, b, pair.PostgresRecord, fixture.Step{Query: "INSERT INTO t VALUES(7)", Affected: 1})
	var before, after int
	if err = a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, ctx, tx, pair.PostgresRecord, fixture.Step{Query: "INSERT INTO t VALUES(1)", Prepared: prepared, Affected: 1})
	// pg_sleep is solely an oracle synchronization statement. Native cancellation
	// uses actual Operation contexts in TestTransactionPublicationLinearization.
	fixture.SessionCancel(t, ctx, a, prepared)
	fixture.RunStep(t, ctx, tx, pair.PostgresRecord, fixture.TransactionError(false, "SELECT v FROM t", fixture.TransactionAborted()))
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, ctx, b, pair.PostgresRecord, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
	if err = a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&after); err != nil || after != before {
		t.Fatal("cancellation replaced healthy backend", before, after, err)
	}
	fixture.RunStep(t, ctx, a, pair.PostgresRecord, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 7))
}
