package reference_test

import "testing"

func TestReferenceDeleteCoreQualifiedCastBoundaries(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  referenceDiagnostic
	}{
		{"DELETE FROM t AS d WHERE d.v::int4=10", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "::"`, Position: "29"}},
		{"DELETE FROM t AS d WHERE CAST(d.v AS int4)=10", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "32"}},
		{"DELETE FROM t AS d WHERE int4(d.v)=10", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "."`, Position: "32"}},
	} {
		for _, prepared := range []bool{false, true} {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name+tc.query, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(4,10),(4,10)", 4))
					if backend.pg {
						s := referenceExec(tc.query, 3)
						s.prepared = prepared
						run(s)
						deleteReferenceRows(t, p, backend.db, backend.recorder, 2)
					} else {
						s := referenceImplicitError(tc.query, tc.want)
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, s)
						deleteReferenceRows(t, p, backend.db, backend.recorder, 1, 2, 4, 4)
					}
				})
			}
		}
	}
}

func TestReferenceDeleteCoreTrailingDiagnostic(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceLockExec(t, p.ctx, backend.db, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, backend.db, "INSERT INTO t VALUES(1,10),(2,20)", 2)
			q := "DELETE FROM t AS d WHERE d.v IS NULL; nonsense"
			for _, prepared := range []bool{false, true} {
				if backend.pg {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: "39"})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					deleteReferenceExecuteStep(t, p, backend.db, backend.recorder, s)
				} else if prepared {
					held, err := backend.db.PrepareContext(p.ctx, q)
					if held != nil {
						held.Close()
						t.Fatal("excluded input prepared")
					}
					if err == nil {
						t.Fatal("excluded input accepted")
					}
				} else {
					result, err := backend.db.ExecContext(p.ctx, q)
					if result != nil || err == nil {
						t.Fatal(result, err)
					}
				}
				deleteReferenceRows(t, p, backend.db, backend.recorder, 1, 2)
			}
		})
	}
}
