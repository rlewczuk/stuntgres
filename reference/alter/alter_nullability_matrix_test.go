package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
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
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(NULL,9)", 1))
						diagnostic := fixture.Diagnostic{Code: "23502", Message: `column "a" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "a"}
						if strings.Contains(q, "DROP COLUMN") {
							diagnostic = fixture.Diagnostic{Code: "42703", Message: `column "a" of relation "t" does not exist`}
						}
						step := fixture.ImplicitError(q, diagnostic)
						step.Prepared = prepared
						step.ErrorStage = "execute"
						run(step)
						fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
						run(fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(9)}}))
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE holder(v INT CONSTRAINT "+existing+" NOT NULL)", 0))
					run(fixture.Exec("CREATE TABLE "+table+"("+a+" INT,"+b+" INT)", 0))
					run(fixture.Exec("INSERT INTO "+table+" VALUES(1,2)", 1))
					step := fixture.Exec("ALTER TABLE "+table+" ALTER "+a+" SET NOT NULL, ALTER "+b+" SET NOT NULL", 0)
					step.Prepared = prepared
					run(step)
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM "+table, []string{a, b}, []string{"INT4", "INT4"}, []bool{false, false}, 1)
					for _, probe := range []struct{ values, column, detail string }{{"NULL,2", a, "Failing row contains (null, 2)."}, {"1,NULL", b, "Failing row contains (1, null)."}} {
						failure := fixture.ImplicitError("INSERT INTO "+table+" VALUES("+probe.values+")", fixture.Diagnostic{Code: "23502", Message: fmt.Sprintf(`null value in column "%s" of relation "%s" violates not-null constraint`, probe.column, table), Detail: probe.detail, Schema: "public", Table: table, Column: probe.column})
						failure.Prepared = prepared
						failure.ErrorStage = "execute"
						run(failure)
					}
					run(fixture.ImplicitError("ALTER TABLE "+table+" DROP CONSTRAINT "+existing, fixture.Diagnostic{Code: "42704", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, existing, table)}))
					step = fixture.Exec("ALTER TABLE "+table+" DROP CONSTRAINT "+first+", DROP CONSTRAINT "+second, 0)
					step.Prepared = prepared
					run(step)
					step = fixture.Exec("INSERT INTO "+table+" VALUES(NULL,NULL)", 1)
					step.Prepared = prepared
					run(step)
					run(fixture.Select("SELECT * FROM "+table+" ORDER BY "+a, []string{a, b}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {nil, nil}}))
					run(fixture.ImplicitError("INSERT INTO holder VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "holder" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "holder", Column: "v"}))
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
					for _, leading := range []string{"", prefix} {
						step := fixture.ImplicitError(leading+q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "RENAME"`, Position: strconv.Itoa(len(leading) + 37)})
						step.Prepared = prepared
						step.ErrorStage = "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
						run(step)
						fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
						run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if leading != "" {
							run(fixture.Exec("CREATE TABLE prefix(a INT)", 0))
							run(fixture.Exec("DROP TABLE prefix", 0))
						}
					}
				})
			}
		})
	}
}
