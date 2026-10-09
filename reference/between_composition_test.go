package reference_test

import "testing"

func TestReferenceBetweenConsumers(t *testing.T) {
	p := betweenReferenceSetup(t)
	betweenReferenceBoth(t, p, referenceSelect("SELECT CASE WHEN v BETWEEN 1 AND 3 THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"case"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}}))
	betweenReferenceBoth(t, p, referenceSelect("SELECT CASE WHEN v BETWEEN 1 AND 3 = TRUE THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"case"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}}))
	betweenReferenceBoth(t, p, referenceSelect("SELECT COALESCE(v BETWEEN 1 AND 3,FALSE) FROM t ORDER BY id", []string{"coalesce"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {true}, {false}, {false}}))
	scalarStep(t, p, referenceExec("CREATE TABLE result(id INTEGER,b BOOLEAN)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO result SELECT id,v BETWEEN 1 AND 3 FROM t", 6))
	want := [][]any{{int64(1), false}, {int64(2), true}, {int64(3), true}, {int64(4), true}, {int64(5), false}, {int64(6), nil}}
	scalarStep(t, p, referenceSelect("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	scalarStep(t, p, referencePrepared(referenceExec("UPDATE result SET b=id BETWEEN 2 AND 4 WHERE id BETWEEN 2 AND 4", 3)))
	scalarStep(t, p, referenceSelect("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"UPDATE result SET b=id BETWEEN 1 AND TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer <= boolean", Hint: hint, Position: "24"}},
		{"UPDATE result SET b=id BETWEEN 7 AND 'oops'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "38"}},
	} {
		step := referenceImplicitError(tc.q, tc.d)
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
		scalarStep(t, p, referenceSelect("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	}
	group := referenceSelect("SELECT v BETWEEN 1 AND 3,count(*) FROM t GROUP BY v BETWEEN 1 AND 3", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(2)}, {true, int64(3)}, {nil, int64(1)}})
	group.bag = true
	betweenReferenceBoth(t, p, group)
	betweenReferenceBoth(t, p, referenceSelect("SELECT count(*) FROM t GROUP BY v BETWEEN 1 AND 3 HAVING v BETWEEN 1 AND 3", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	scalarStep(t, p, referenceExec("CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN 1 AND 3),s BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),n BOOL DEFAULT (2 NOT BETWEEN 1 AND 3))", 0))
	scalarStep(t, p, referenceExec("INSERT INTO defaults DEFAULT VALUES", 1))
	scalarStep(t, p, referenceSelect("SELECT * FROM defaults", []string{"b", "s", "n"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
	scalarStep(t, p, referenceExec("DELETE FROM t WHERE v BETWEEN SYMMETRIC 3 AND 1 = TRUE", 3))
	scalarStep(t, p, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(6)}}))
	scalarStep(t, p, referencePrepared(referenceExec("DELETE FROM t WHERE v NOT BETWEEN 1 AND 3", 2)))
	scalarStep(t, p, referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
}
