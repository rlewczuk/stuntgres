package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreMixedParameterPolicyIsolation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,f BOOL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,TRUE)", 1))
			sentinel := fixture.Select("SELECT a,f FROM t", []string{"a", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			for _, q := range []string{"UPDATE t SET a=$1,f=$1", "INSERT INTO t VALUES($1,$1)"} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}
				if q[0] == 'I' {
					if backend.PG {
						want.Position = "25"
					} else {
						want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "parameter reused with different destination types"}
					}
				}
				for _, prepared := range []bool{false, true} {
					if prepared {
						stmt, err := backend.DB.PrepareContext(p.Ctx, q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("mixed signature prepared")
						}
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						result, err := backend.DB.ExecContext(p.Ctx, q, 1)
						if result != nil {
							t.Fatal("failed policy returned result")
						}
						fixture.AssertDiagnostic(t, err, &want)
					}
					run(sentinel)
				}
			}
		})
	}
}

func TestReferenceUpdateCoreSuffixBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT,n SMALLINT NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
			for i, q := range []string{"UPDATE t SET a=9 FROM t", "UPDATE t SET a=9 trailing", "UPDATE t SET a=9; SELECT a FROM t"} {
				for _, prepared := range []bool{false, true} {
					run(fixture.Exec("UPDATE t SET a=1", 1))
					a := int64(1)
					if i == 0 {
						want := fixture.ControlsReferenceDiagnostic("42712", `table name "t" specified more than once`)
						step := fixture.ErrorDiagnostic(q, want)
						if prepared {
							step = fixture.Prepared(step)
							step.ErrorStage = "prepare"
						}
						run(step)
						run(fixture.Select("SELECT a,n FROM t", []string{"a", "n"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}}))
						continue
					}
					if i == 2 && !prepared {
						run(fixture.Exec(q, 1))
						a = 9
					} else {
						want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
						want.Position = "18"
						if i == 2 {
							want.Message = "cannot insert multiple commands into a prepared statement"
							want.Position = ""
						}
						step := fixture.ErrorDiagnostic(q, want)
						if prepared {
							step = fixture.Prepared(step)
							step.ErrorStage = "prepare"
						}
						run(step)
					}
					run(fixture.Select("SELECT a,n FROM t", []string{"a", "n"}, []string{"INT4", "INT2"}, [][]any{{a, int64(1)}}))
				}
			}
			run(fixture.Exec("UPDATE t SET a=1", 1))
			run(fixture.ImplicitError("UPDATE t SET a=$1; SELECT a FROM t", fixture.Diagnostic{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}, 9))
			run(fixture.Select("SELECT a,n FROM t", []string{"a", "n"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}}))
		})
	}
}
