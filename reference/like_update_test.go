//go:build reference

package reference_test

import "testing"

func TestReferenceLikeOracleUpdate(t *testing.T) { likeReferenceUpdate(t, true) }
func TestReferenceLikeUpdate(t *testing.T)       { likeReferenceUpdate(t, false) }
func likeReferenceUpdate(t *testing.T, oracleOnly bool) {
	p := newReferencePair(t)
	run := func(s referenceStep) {
		if oracleOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(referenceExec("CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT,mark INT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,'a%b','a!%b','!',0),(2,'Ab','a%','!',0),(3,NULL,NULL,NULL,0)", 3))
	for _, tc := range []struct {
		predicate string
		args      []any
		count     int64
		marks     [][]any
	}{
		{"v LIKE 'a%'", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
		{"v ILIKE $1", []any{"a%"}, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
		{"v NOT LIKE 'a%'", nil, 1, [][]any{{int64(0)}, {int64(1)}, {int64(0)}}},
		{"v NOT ILIKE 'a%'", nil, 0, [][]any{{int64(0)}, {int64(0)}, {int64(0)}}},
		{"v LIKE p ESCAPE e", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
		{"v ILIKE p ESCAPE e", nil, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
	} {
		t.Run(tc.predicate, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				run(referenceExec("UPDATE t SET mark=0", 3))
				s := referenceExec("UPDATE t SET mark=1 WHERE "+tc.predicate, tc.count, tc.args...)
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
				run(referenceSelect("SELECT mark FROM t ORDER BY id", []string{"mark"}, []string{"INT4"}, tc.marks))
				selected := referenceSelect("SELECT id FROM t WHERE "+tc.predicate, []string{"id"}, []string{"INT4"}, nil, tc.args...)
				for i, row := range tc.marks {
					if row[0] == int64(1) {
						selected.rows = append(selected.rows, []any{int64(i + 1)})
					}
				}
				selected.query += " ORDER BY id"
				run(selected)
				run(referencePrepared(selected))
			}
		})
	}
	run(referenceExec("UPDATE t SET mark=0", 3))
	run(referenceExec("UPDATE t SET e='ab' WHERE id=2", 1))
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError("UPDATE t SET mark=1 WHERE v ILIKE p ESCAPE e", referenceDiagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
		s.errorStage = "execute"
		if prepared {
			s = referencePrepared(s)
		}
		run(s)
		run(referenceSelect("SELECT mark FROM t ORDER BY id", []string{"mark"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(0)}}))
	}
}
