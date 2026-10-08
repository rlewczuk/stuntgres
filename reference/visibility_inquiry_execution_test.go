//go:build reference

package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceVisibilityInquiryChecksAndViews(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, db, rec, s) }
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
			run(referencePrepared(selected))
			if backend == "Native" {
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
			if backend == "Oracle" {
				run(referenceExec("DROP VIEW visibility_view", 0))
				run(referenceExec("DROP TABLE visibility_checks", 0))
			} else if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceVisibilityInquiryEmptySourceConversion(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, db, rec, s) }
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
			run(referenceExec("DROP TABLE visibility_empty", 0))
		})
	}
}
