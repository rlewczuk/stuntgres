//go:build reference

package reference_test

import "testing"

// Native INHERITS remains explicitly unsupported. These independent release
// witnesses exercise the real PostgreSQL descendant contract, not native parity.
func TestReferenceAlterDefaultInheritanceRecursion(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	run(referenceExec("CREATE TABLE p(id INT,i INT DEFAULT 1)", 0))
	run(referenceExec("CREATE TABLE c(i INT DEFAULT 9) INHERITS(p)", 0))
	run(referenceExec("INSERT INTO p VALUES(0,1)", 1))
	run(referenceExec("INSERT INTO c VALUES(10,9)", 1))
	run(referenceExec("ALTER TABLE p ALTER i SET DEFAULT 7", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(1)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(11)", 1))
	run(referenceExec("ALTER TABLE ONLY p ALTER i SET DEFAULT 8", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(2)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(12)", 1))
	run(referenceExec("ALTER TABLE p ALTER i DROP DEFAULT", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(3)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(13)", 1))
	run(referenceExec("ALTER TABLE p ALTER i SET DEFAULT 6", 0))
	run(referenceExec("ALTER TABLE ONLY p ALTER i DROP DEFAULT", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(4)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(14)", 1))
	run(referenceSelect("SELECT * FROM ONLY p ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}, {int64(1), int64(7)}, {int64(2), int64(8)}, {int64(3), nil}, {int64(4), nil}}))
	run(referenceSelect("SELECT * FROM ONLY c ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(9)}, {int64(11), int64(7)}, {int64(12), int64(7)}, {int64(13), nil}, {int64(14), int64(6)}}))
	run(referenceImplicitError("ALTER TABLE p ALTER i SET DEFAULT (SELECT 1)", referenceDiagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression"}))
}
