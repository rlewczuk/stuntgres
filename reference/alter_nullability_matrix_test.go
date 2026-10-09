package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceAlterNullabilityReviewActionMatrix(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"ALTER TABLE t ALTER a SET NOT NULL, ALTER a DROP NOT NULL",
			"ALTER TABLE t ALTER a DROP NOT NULL, ALTER a SET NOT NULL",
			"ALTER TABLE t ALTER a SET NOT NULL, DROP COLUMN a",
			"ALTER TABLE t DROP COLUMN a, ALTER a SET NOT NULL",
		} {
			t.Run(fmt.Sprint(prepared)+q, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
						run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
						run(referenceExec("INSERT INTO t VALUES(NULL,9)", 1))
						diagnostic := referenceDiagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}
						if strings.Contains(q, "DROP COLUMN") {
							diagnostic = referenceDiagnostic{Code: "42703", Message: `column "a" of relation "t" does not exist`}
						}
						step := referenceImplicitError(q, diagnostic)
						step.prepared = prepared
						step.errorStage = "execute"
						run(step)
						referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
						run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(9)}}))
					})
				}
			})
		}
	}
}

func TestReferenceAlterNullabilityReviewGeneratedCollision(t *testing.T) {
	table := strings.Repeat("t", 40)
	a := strings.Repeat("a", 39) + "x"
	b := strings.Repeat("a", 39) + "y"
	existing := strings.Repeat("t", 27) + "_" + strings.Repeat("a", 26) + "_not_null"
	first := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null1"
	second := strings.Repeat("t", 26) + "_" + strings.Repeat("a", 26) + "_not_null2"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE holder(v INT CONSTRAINT "+existing+" NOT NULL)", 0))
					run(referenceExec("CREATE TABLE "+table+"("+a+" INT,"+b+" INT)", 0))
					run(referenceExec("INSERT INTO "+table+" VALUES(1,2)", 1))
					step := referenceExec("ALTER TABLE "+table+" ALTER "+a+" SET NOT NULL, ALTER "+b+" SET NOT NULL", 0)
					step.prepared = prepared
					run(step)
					referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, "SELECT * FROM "+table, []string{a, b}, []string{"INT4", "INT4"}, []bool{false, false}, 1)
					for _, probe := range []struct{ values, column, detail string }{{"NULL,2", a, "Failing row contains (null, 2)."}, {"1,NULL", b, "Failing row contains (1, null)."}} {
						failure := referenceImplicitError("INSERT INTO "+table+" VALUES("+probe.values+")", referenceDiagnostic{Code: "23502", Message: fmt.Sprintf(`null value in column "%s" of relation "%s" violates not-null constraint`, probe.column, table), Detail: probe.detail, Schema: "public", Table: table, Column: probe.column})
						failure.prepared = prepared
						failure.errorStage = "execute"
						run(failure)
					}
					run(referenceImplicitError("ALTER TABLE "+table+" DROP CONSTRAINT "+existing, referenceDiagnostic{Code: "42704", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, existing, table)}))
					step = referenceExec("ALTER TABLE "+table+" DROP CONSTRAINT "+first+", DROP CONSTRAINT "+second, 0)
					step.prepared = prepared
					run(step)
					step = referenceExec("INSERT INTO "+table+" VALUES(NULL,NULL)", 1)
					step.prepared = prepared
					run(step)
					run(referenceSelect("SELECT * FROM "+table+" ORDER BY "+a, []string{a, b}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {nil, nil}}))
					run(referenceImplicitError("INSERT INTO holder VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "holder" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "holder", Column: "v"}))
				})
			}
		})
	}
}

func TestReferenceAlterNullabilityReviewRenameWholeInput(t *testing.T) {
	q := "ALTER TABLE t ALTER a SET NOT NULL, RENAME COLUMN a TO x"
	prefix := "CREATE TABLE prefix(a INT);INSERT INTO prefix VALUES(1);"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(a INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7)", 1))
					for _, leading := range []string{"", prefix} {
						step := referenceImplicitError(leading+q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "RENAME"`, Position: strconv.Itoa(len(leading) + 37)})
						step.prepared = prepared
						step.errorStage = "execute"
						if prepared {
							step.errorStage = "prepare"
						}
						run(step)
						referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
						run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if leading != "" {
							run(referenceExec("CREATE TABLE prefix(a INT)", 0))
							run(referenceExec("DROP TABLE prefix", 0))
						}
					}
				})
			}
		})
	}
}
