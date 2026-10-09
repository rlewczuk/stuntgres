package reference_test

import "testing"

func TestReferenceUpdateCoreMixedParameterPolicyIsolation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,f BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,TRUE)", 1))
			sentinel := referenceSelect("SELECT a,f FROM t", []string{"a", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}})
			for _, q := range []string{"UPDATE t SET a=$1,f=$1", "INSERT INTO t VALUES($1,$1)"} {
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus boolean", Position: "21"}
				if q[0] == 'I' {
					if backend.pg {
						want.Position = "25"
					} else {
						want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "parameter reused with different destination types"}
					}
				}
				for _, prepared := range []bool{false, true} {
					if prepared {
						stmt, err := backend.db.PrepareContext(p.ctx, q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("mixed signature prepared")
						}
						referenceAssertDiagnostic(t, err, &want)
					} else {
						result, err := backend.db.ExecContext(p.ctx, q, 1)
						if result != nil {
							t.Fatal("failed policy returned result")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					run(sentinel)
				}
			}
		})
	}
}

func TestReferenceUpdateCoreSuffixBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT,n SMALLINT NOT NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,1)", 1))
			for i, q := range []string{"UPDATE t SET a=9 FROM t", "UPDATE t SET a=9 trailing", "UPDATE t SET a=9; SELECT a FROM t"} {
				for _, prepared := range []bool{false, true} {
					run(referenceExec("UPDATE t SET a=1", 1))
					a := int64(1)
					if i == 0 {
						want := controlsReferenceDiagnostic("42712", `table name "t" specified more than once`)
						step := referenceErrorDiagnostic(q, want)
						if prepared {
							step = referencePrepared(step)
							step.errorStage = "prepare"
						}
						run(step)
						run(referenceSelect("SELECT a,n FROM t", []string{"a", "n"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}}))
						continue
					}
					if !backend.pg {
						if _, err := backend.db.ExecContext(p.ctx, q); err == nil {
							t.Fatal("excluded suffix executed", q)
						}
					} else {
						if i == 2 && !prepared {
							run(referenceExec(q, 1))
							a = 9
						} else {
							want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42712", Message: `table name "t" specified more than once`}
							if i == 1 {
								want.Code = "42601"
								want.Message = `syntax error at or near "trailing"`
								want.Position = "18"
							}
							if i == 2 {
								want.Code = "42601"
								want.Message = "cannot insert multiple commands into a prepared statement"
							}
							if prepared {
								stmt, err := backend.db.PrepareContext(p.ctx, q)
								if stmt != nil {
									stmt.Close()
									t.Fatal("invalid suffix prepared")
								}
								referenceAssertDiagnostic(t, err, &want)
							} else {
								result, err := backend.db.ExecContext(p.ctx, q)
								if result != nil {
									t.Fatal("invalid suffix returned result")
								}
								referenceAssertDiagnostic(t, err, &want)
							}
						}
					}
					run(referenceSelect("SELECT a,n FROM t", []string{"a", "n"}, []string{"INT4", "INT2"}, [][]any{{a, int64(1)}}))
				}
			}
		})
	}
}
