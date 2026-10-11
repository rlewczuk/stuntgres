package reference_test

import (
	"bytes"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVisibilityInquiryChecksAndViews(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, rec := backend.DB, backend.Recorder
					run := func(s fixture.Step) {
						t.Helper()
						s.CheckMetadata = fixture.VisibilityBoolMetadata
						s.NonContext = source
						fixture.RunStep(t, p.Ctx, db, rec, s)
					}
					run(fixture.Exec("CREATE TABLE visibility_checks(id OID CONSTRAINT known CHECK(pg_type_is_visible(id) IS NOT NULL))", 0))
					bad := fixture.ImplicitError("INSERT INTO visibility_checks VALUES(23),(0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "visibility_checks" violates check constraint "known"`, Detail: "Failing row contains (0).", Schema: "public", Table: "visibility_checks", Constraint: "known"})
					bad.ErrorStage = "execute"
					run(bad)
					run(fixture.Prepared(bad))
					run(fixture.Select("SELECT count(*) FROM visibility_checks", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("INSERT INTO visibility_checks VALUES(23)", 1))
					run(fixture.Select("SELECT pg_type_is_visible(id) FROM visibility_checks", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Select("UPDATE visibility_checks SET id=26 RETURNING pg_type_is_visible(id)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Exec("CREATE VIEW visibility_view AS SELECT pg_type_is_visible(id) AS visible,pg_table_is_visible(NULL) AS absent FROM visibility_checks", 0))
					selected := fixture.Select("SELECT visible,absent FROM visibility_view", []string{"visible", "absent"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}})
					run(selected)
					if !source {
						run(fixture.Prepared(selected))
					}
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						run(selected)
						run(fixture.Select("SELECT pg_collation_is_visible(6411),pg_conversion_is_visible(4402)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
					}
					run(fixture.Select("DELETE FROM visibility_checks RETURNING pg_type_is_visible(id)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					run(fixture.Select("SELECT pg_type_is_visible(id) FROM visibility_checks", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{}))
					if backend.PG {
						run(fixture.Exec("DROP VIEW visibility_view", 0))
						run(fixture.Exec("DROP TABLE visibility_checks", 0))
					} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					backend.DB.SetMaxOpenConns(1)
					var conn *sql.Conn
					var executor fixture.Executor = backend.DB
					if !source {
						var err error
						conn, err = backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						executor = conn
					}
					rec := backend.Recorder
					run := func(s fixture.Step) {
						t.Helper()
						s.CheckMetadata = fixture.VisibilityBoolMetadata
						s.NonContext = source
						fixture.RunStep(t, p.Ctx, executor, rec, s)
					}
					run(fixture.Exec("CREATE TABLE visibility_empty(id OID)", 0))
					for _, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
						bad := fixture.ImplicitError("SELECT "+fn+"('bad') FROM visibility_empty", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: strconv.Itoa(9 + len(fn))})
						bad.SelectRows = true
						bad.ErrorStage = "execute"
						run(bad)
						bad.ErrorStage = "prepare"
						run(fixture.Prepared(bad))
						run(fixture.Select("SELECT "+fn+"(id) FROM visibility_empty", []string{fn}, []string{"BOOL"}, [][]any{}))
						rangeError := fixture.ImplicitError("SELECT "+fn+"(4294967296::bigint)", fixture.Diagnostic{Code: "22003", Message: "OID out of range"})
						rangeError.SelectRows = true
						rangeError.ErrorStage = "execute"
						run(rangeError)
						run(fixture.Prepared(rangeError))
						emptyRange := rangeError
						emptyRange.Query = "SELECT " + fn + "(4294967296::bigint) FROM visibility_empty"
						emptyRange.SelectRows = false
						run(emptyRange)
						emptyRange.SelectRows = true
						run(fixture.Prepared(emptyRange))
						args := strings.Repeat("1,", 100) + "missing_column"
						query := "SELECT " + fn + "(" + args + ") FROM visibility_empty"
						missing := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: strconv.Itoa(strings.Index(query, "missing_column") + 1)})
						missing.SelectRows = true
						missing.ErrorStage = "execute"
						run(missing)
						missing.ErrorStage = "prepare"
						run(fixture.Prepared(missing))
						importError := fixture.ImplicitError("SELECT "+fn+"(NULL),$1::oid FROM visibility_empty", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
						importError.SelectRows = true
						importError.ErrorStage = "execute"
						run(fixture.Prepared(importError))
					}
					var before, after string
					if !source {
						if err := conn.QueryRowContext(p.Ctx, "SELECT current_schemas(true)").Scan(&before); err != nil || before != "{pg_catalog,public}" {
							t.Fatal(before, err)
						}
					} else {
						run(fixture.Select("SELECT current_schemas(true)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{pg_catalog,public}"}}))
					}
					run(fixture.Select("SELECT pg_type_is_visible(23)", []string{"pg_type_is_visible"}, []string{"BOOL"}, [][]any{{true}}))
					if !source {
						if err := conn.QueryRowContext(p.Ctx, "SELECT current_schemas(true)").Scan(&after); err != nil || after != before {
							t.Fatal(after, before, err)
						}
					} else {
						run(fixture.Select("SELECT current_schemas(true)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{pg_catalog,public}"}}))
					}
					run(fixture.Exec("DROP TABLE visibility_empty", 0))
				})
			}
		})
	}
}
