package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceVisibilityInquiryChecksAndViews(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db, rec := backend.db, backend.recorder
					run := func(s referenceStep) {
						t.Helper()
						s.checkMetadata = visibilityBoolMetadata
						s.nonContext = source
						referenceRunStep(t, p.ctx, db, rec, s)
					}
					run(referenceExec("CREATE TABLE visibility_checks(id OID CONSTRAINT known CHECK(pg_type_is_visible(id) IS NOT NULL))", 0))
					bad := referenceImplicitError("INSERT INTO visibility_checks VALUES(23),(0)", referenceDiagnostic{Code: "23514", Message: `new row for relation "visibility_checks" violates check constraint "known"`, Detail: "Failing row contains (0).", Schema: "public", Table: "visibility_checks", Constraint: "known"})
					bad.errorStage = "execute"
					run(bad)
					run(referencePrepared(bad))
					run(referenceSelect("SELECT count(*) FROM visibility_checks", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(referenceExec("INSERT INTO visibility_checks VALUES(23)", 1))
					run(referenceSelect("SELECT pg_type_is_visible(id) FROM visibility_checks", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(referenceSelect("UPDATE visibility_checks SET id=26 RETURNING pg_type_is_visible(id)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(referenceExec("CREATE VIEW visibility_view AS SELECT pg_type_is_visible(id) AS visible,pg_table_is_visible(NULL) AS absent FROM visibility_checks", 0))
					selected := referenceSelect("SELECT visible,absent FROM visibility_view", []string{"visible", "absent"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}})
					run(selected)
					if !source {
						run(referencePrepared(selected))
					}
					if !backend.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						run(selected)
						run(referenceSelect("SELECT pg_collation_is_visible(6411),pg_conversion_is_visible(4402)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					}
					run(referenceSelect("DELETE FROM visibility_checks RETURNING pg_type_is_visible(id)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(referenceSelect("SELECT pg_type_is_visible(id) FROM visibility_checks", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{}))
					if backend.pg {
						run(referenceExec("DROP VIEW visibility_view", 0))
						run(referenceExec("DROP TABLE visibility_checks", 0))
					} else if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestReferenceVisibilityInquiryEmptySourceConversion(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					backend.db.SetMaxOpenConns(1)
					var conn *sql.Conn
					var executor referenceExecutor = backend.db
					if !source {
						var err error
						conn, err = backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						executor = conn
					}
					rec := backend.recorder
					run := func(s referenceStep) {
						t.Helper()
						s.checkMetadata = visibilityBoolMetadata
						s.nonContext = source
						referenceRunStep(t, p.ctx, executor, rec, s)
					}
					run(referenceExec("CREATE TABLE visibility_empty(id OID)", 0))
					for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
						bad := referenceImplicitError("SELECT "+fn+"('bad') FROM visibility_empty", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))})
						bad.selectRows = true
						bad.errorStage = "execute"
						run(bad)
						bad.errorStage = "prepare"
						run(referencePrepared(bad))
						run(referenceSelect("SELECT "+fn+"(id) FROM visibility_empty", []string{fn}, []string{"BOOL"}, [][]any{}))
						rangeError := referenceImplicitError("SELECT "+fn+"(4294967296::bigint)", referenceDiagnostic{Code: "22003", Message: "OID out of range"})
						rangeError.selectRows = true
						rangeError.errorStage = "execute"
						run(rangeError)
						run(referencePrepared(rangeError))
						emptyRange := rangeError
						emptyRange.query = "SELECT " + fn + "(4294967296::bigint) FROM visibility_empty"
						emptyRange.selectRows = false
						run(emptyRange)
						emptyRange.selectRows = true
						run(referencePrepared(emptyRange))
						args := strings.Repeat("1,", 100) + "missing_column"
						query := "SELECT " + fn + "(" + args + ") FROM visibility_empty"
						missing := referenceImplicitError(query, referenceDiagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: strconv.Itoa(strings.Index(query, "missing_column") + 1)})
						missing.selectRows = true
						missing.errorStage = "execute"
						run(missing)
						missing.errorStage = "prepare"
						run(referencePrepared(missing))
						importError := referenceImplicitError("SELECT "+fn+"(NULL),$1::oid FROM visibility_empty", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
						importError.selectRows = true
						importError.errorStage = "execute"
						run(referencePrepared(importError))
					}
					var before, after string
					if !source {
						if err := conn.QueryRowContext(p.ctx, "SELECT current_schemas(true)").Scan(&before); err != nil || before != "{pg_catalog,public}" {
							t.Fatal(before, err)
						}
					} else {
						run(referenceSelect("SELECT current_schemas(true)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{pg_catalog,public}"}}))
					}
					run(referenceSelect("SELECT pg_type_is_visible(23)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					if !source {
						if err := conn.QueryRowContext(p.ctx, "SELECT current_schemas(true)").Scan(&after); err != nil || after != before {
							t.Fatal(after, before, err)
						}
					} else {
						run(referenceSelect("SELECT current_schemas(true)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{pg_catalog,public}"}}))
					}
					run(referenceExec("DROP TABLE visibility_empty", 0))
				})
			}
		})
	}
}
