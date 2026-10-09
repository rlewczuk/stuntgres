package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceNotNullAlterNoticesAndOwnership(t *testing.T) {
	roleReferenceCleanup(t, []string{"nn_reader", "nn_schema_owner"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "nn_schema")
	for _, backend := range p.backends() {
		postgres := backend.pg
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, conn, record := noticeReferencePool(t, p, postgres, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
			// Provision this notice connector's own native DSN, not the parent pair.
			run(referenceExec("CREATE USER nn_reader", 0))
			run(referenceExec("CREATE USER nn_schema_owner", 0))
			run(referenceExec("CREATE SCHEMA nn_schema AUTHORIZATION nn_schema_owner", 0))
			run(referenceExec("CREATE TABLE t(a INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7)", 1))
			run(referenceExec("CREATE TABLE nn_schema.t(a INT)", 0))
			if postgres {
				// Fixture recreation removes public's default USAGE; reach owner-only ALTER.
				run(referenceExec("GRANT USAGE ON SCHEMA public TO nn_reader", 0))
			}
			for _, prepared := range []bool{false, true} {
				notices = nil
				s := referenceExec("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", 0)
				s.prepared = prepared
				run(s)
				want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices)
				}
			}
			run(referenceExec("SET client_min_messages TO warning", 0))
			notices = nil
			run(referenceExec("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", 0))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			run(referenceExec("RESET client_min_messages", 0))
			run(referenceExec("SET SESSION AUTHORIZATION nn_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "must be owner of table t"})
				s.prepared = true
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("SET SESSION AUTHORIZATION nn_schema_owner", 0))
			run(referenceImplicitError("ALTER TABLE nn_schema.t ALTER a SET NOT NULL", referenceDiagnostic{Code: "42501", Message: "must be owner of table t"}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceNotNullExternalTransactionalDDL(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	for _, backend := range p.backends() {
		db := backend.db
		conn, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			record := backend.recorder
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				tx, err := conn.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, record, referenceExec(q, 0))
				referenceRunStep(t, p.ctx, tx, record, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if q == "ALTER TABLE t ALTER a SET NOT NULL" || q == "ALTER TABLE t ADD NOT NULL a" || q == "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a" {
					referenceRunStep(t, p.ctx, tx, record, referenceImplicitError("INSERT INTO t VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			referenceTableDDLMetadata(t, p.ctx, conn, backend.pg, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
			referenceRunStep(t, p.ctx, conn, record, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			referenceRunStep(t, p.ctx, conn, record, referenceExec("INSERT INTO t VALUES(NULL)", 1))
		}()
	}
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
}

func TestReferenceNotNullRetainedParameterizedStatements(t *testing.T) {
	for _, preparedUtility := range []bool{false, true} {
		for _, add := range []bool{false, true} {
			t.Run(fmt.Sprint(preparedUtility, "/", add), func(t *testing.T) {
				p := newReferenceScenario(t)
				p.step(t, referenceExec("CREATE TABLE t(a INT,b TEXT)", 0))
				p.step(t, referenceExec("INSERT INTO t VALUES(7,'seed')", 1))
				for _, backend := range p.backends() {
					func() {
						ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
						selectStmt, err := backend.db.PrepareContext(ctx, "SELECT * FROM t ORDER BY b")
						if err != nil {
							t.Fatal(err)
						}
						defer selectStmt.Close()
						insertStmt, err := backend.db.PrepareContext(ctx, "INSERT INTO t VALUES($1,$2)")
						if err != nil {
							t.Fatal(err)
						}
						defer insertStmt.Close()
						seed := [][]any{{int64(7), "seed"}}
						referenceNotNullRetainedRows(t, ctx, selectStmt, !backend.pg, true, seed)
						install, drop := "ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL"
						if add {
							install, drop = "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", "ALTER TABLE t DROP CONSTRAINT nn"
						}
						utility := func(q string) {
							s := referenceExec(q, 0)
							s.prepared = preparedUtility
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						}
						utility(install)
						want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
						_, err = backend.db.ExecContext(ctx, "INSERT INTO t VALUES($1,$2)", nil, "bad")
						referenceAssertDiagnostic(t, err, &want)
						referenceNotNullRetainedRows(t, ctx, selectStmt, !backend.pg, false, seed)
						_, err = insertStmt.ExecContext(ctx, nil, "bad")
						referenceAssertDiagnostic(t, err, &want)
						referenceNotNullRetainedRows(t, ctx, selectStmt, !backend.pg, false, seed)
						insert := func(a any, b string) {
							result, err := insertStmt.ExecContext(ctx, a, b)
							if err != nil {
								t.Fatal(err)
							}
							count, err := result.RowsAffected()
							if err != nil || count != 1 {
								t.Fatal(count, err)
							}
						}
						insert(int64(8), "valid")
						referenceNotNullRetainedRows(t, ctx, selectStmt, !backend.pg, false, [][]any{{int64(7), "seed"}, {int64(8), "valid"}})
						utility(drop)
						insert(nil, "after")
						referenceNotNullRetainedRows(t, ctx, selectStmt, !backend.pg, true, [][]any{{nil, "after"}, {int64(7), "seed"}, {int64(8), "valid"}})
					}()
				}
				p.step(t, referenceSelect("SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "after"}, {int64(7), "seed"}, {int64(8), "valid"}}))
			})
		}
	}
}

func referenceNotNullRetainedRows(t *testing.T, ctx context.Context, stmt *sql.Stmt, native, nullable bool, want [][]any) {
	t.Helper()
	rows, err := stmt.QueryContext(ctx)
	if err != nil {
		t.Fatal("retained SELECT", err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		wantType := "INT4"
		wantScan := reflect.TypeFor[int32]()
		wantNullable := false
		if native {
			wantScan = reflect.TypeFor[int64]()
			wantNullable = nullable
		}
		if i == 1 {
			wantType = "TEXT"
			wantScan = reflect.TypeFor[string]()
			wantNullable = native
		}
		gotNullable, known := typ.Nullable()
		// pq exposes INT4's int32 ScanType, but no Nullable capability. Native
		// exposes int64 and current known nullability; these are not parity fields.
		if typ.DatabaseTypeName() != wantType || typ.ScanType() != wantScan || known != native || gotNullable != wantNullable {
			t.Fatal(native, i, typ.DatabaseTypeName(), typ.ScanType(), gotNullable, known)
		}
	}
	var got [][]any
	for rows.Next() {
		var a, b any
		if err = rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		referenceAssertRawValue(t, !native, "INT4", a)
		referenceAssertRawValue(t, !native, "TEXT", b)
		got = append(got, []any{referenceNormalize(a), referenceNormalize(b)})
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal("retained cursor close", err)
	}
	record, ok := ctx.Value(referenceRecorderKey{}).(*referenceRecorder)
	if !ok {
		t.Fatal("retained SELECT requires the owning backend recorder context")
	}
	record.mu.Lock()
	var recorded *referenceOperation
	for _, op := range record.operations {
		if op.Err == nil && reflect.DeepEqual(op.Columns, []string{"a", "b"}) {
			recorded = op
		}
	}
	if recorded == nil {
		record.mu.Unlock()
		t.Fatal("retained SELECT has no recorded result")
	}
	capturedRows, closed, closeErr := recorded.Rows, recorded.Closed, recorded.CloseErr
	record.mu.Unlock()
	captured := make([][]any, 0, len(capturedRows))
	for _, row := range capturedRows {
		if len(row) != 2 {
			t.Fatal("recorded retained row width", row)
		}
		referenceAssertRawValue(t, !native, "INT4", row[0])
		referenceAssertRawValue(t, !native, "TEXT", row[1])
		captured = append(captured, []any{referenceNormalize(row[0]), referenceNormalize(row[1])})
	}
	if !reflect.DeepEqual(captured, want) || !closed || closeErr != nil {
		t.Fatal("recorded retained rows/closure", captured, want, closed, closeErr)
	}
}
