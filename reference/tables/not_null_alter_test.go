package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNotNullAlterNoticesAndOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nn_reader", "nn_schema_owner"})
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "nn_schema")
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
			// Provision this notice connector's own native DSN, not the parent pair.
			run(fixture.Exec("CREATE USER nn_reader", 0))
			run(fixture.Exec("CREATE USER nn_schema_owner", 0))
			run(fixture.Exec("CREATE SCHEMA nn_schema AUTHORIZATION nn_schema_owner", 0))
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
			run(fixture.Exec("CREATE TABLE nn_schema.t(a INT)", 0))
			if postgres {
				// Fixture recreation removes public's default USAGE; reach owner-only ALTER.
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO nn_reader", 0))
			}
			for _, prepared := range []bool{false, true} {
				notices = nil
				s := fixture.Exec("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", 0)
				s.Prepared = prepared
				run(s)
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices)
				}
			}
			run(fixture.Exec("SET client_min_messages TO warning", 0))
			notices = nil
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", 0))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			run(fixture.Exec("RESET client_min_messages", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION nn_reader", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"})
				s.Prepared = true
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION nn_schema_owner", 0))
			run(fixture.ImplicitError("ALTER TABLE nn_schema.t ALTER a SET NOT NULL", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceNotNullExternalTransactionalDDL(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	for _, backend := range p.Backends() {
		db := backend.DB
		conn, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			record := backend.Recorder
			for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, record, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, tx, record, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				if q == "ALTER TABLE t ALTER a SET NOT NULL" || q == "ALTER TABLE t ADD NOT NULL a" || q == "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a" {
					fixture.RunStep(t, p.Ctx, tx, record, fixture.ImplicitError("INSERT INTO t VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			fixture.TableDDLMetadata(t, p.Ctx, conn, backend.PG, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
		}()
	}
	p.Step(t, fixture.Select("SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
}

func TestReferenceNotNullRetainedParameterizedStatements(t *testing.T) {
	for _, preparedUtility := range []bool{false, true} {
		for _, add := range []bool{false, true} {
			t.Run(fmt.Sprint(preparedUtility, "/", add), func(t *testing.T) {
				p := fixture.NewScenario(t)
				p.Step(t, fixture.Exec("CREATE TABLE t(a INT,b TEXT)", 0))
				p.Step(t, fixture.Exec("INSERT INTO t VALUES(7,'seed')", 1))
				for _, backend := range p.Backends() {
					func() {
						ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
						selectStmt, err := backend.DB.PrepareContext(ctx, "SELECT * FROM t ORDER BY b")
						if err != nil {
							t.Fatal(err)
						}
						defer selectStmt.Close()
						insertStmt, err := backend.DB.PrepareContext(ctx, "INSERT INTO t VALUES($1,$2)")
						if err != nil {
							t.Fatal(err)
						}
						defer insertStmt.Close()
						seed := [][]any{{int64(7), "seed"}}
						fixture.NotNullRetainedRows(t, ctx, selectStmt, !backend.PG, true, seed)
						install, drop := "ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL"
						if add {
							install, drop = "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a", "ALTER TABLE t DROP CONSTRAINT nn"
						}
						utility := func(q string) {
							s := fixture.Exec(q, 0)
							s.Prepared = preparedUtility
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						}
						utility(install)
						want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
						_, err = backend.DB.ExecContext(ctx, "INSERT INTO t VALUES($1,$2)", nil, "bad")
						fixture.AssertDiagnostic(t, err, &want)
						fixture.NotNullRetainedRows(t, ctx, selectStmt, !backend.PG, false, seed)
						_, err = insertStmt.ExecContext(ctx, nil, "bad")
						fixture.AssertDiagnostic(t, err, &want)
						fixture.NotNullRetainedRows(t, ctx, selectStmt, !backend.PG, false, seed)
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
						fixture.NotNullRetainedRows(t, ctx, selectStmt, !backend.PG, false, [][]any{{int64(7), "seed"}, {int64(8), "valid"}})
						utility(drop)
						insert(nil, "after")
						fixture.NotNullRetainedRows(t, ctx, selectStmt, !backend.PG, true, [][]any{{nil, "after"}, {int64(7), "seed"}, {int64(8), "valid"}})
					}()
				}
				p.Step(t, fixture.Select("SELECT * FROM t ORDER BY b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, "after"}, {int64(7), "seed"}, {int64(8), "valid"}}))
			})
		}
	}
}
