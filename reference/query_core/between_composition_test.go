package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenConsumers(t *testing.T) {
	p := betweenReferenceSetup(t)
	betweenReferenceBoth(t, p, fixture.Select("SELECT CASE WHEN v BETWEEN 1 AND 3 THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"case"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}}))
	betweenReferenceBoth(t, p, fixture.Select("SELECT CASE WHEN v BETWEEN 1 AND 3 = TRUE THEN 1 ELSE 0 END FROM t ORDER BY id", []string{"case"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(0)}, {int64(0)}}))
	betweenReferenceBoth(t, p, fixture.Select("SELECT COALESCE(v BETWEEN 1 AND 3,FALSE) FROM t ORDER BY id", []string{"coalesce"}, []string{"BOOL"}, [][]any{{false}, {true}, {true}, {true}, {false}, {false}}))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE result(id INTEGER,b BOOLEAN)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO result SELECT id,v BETWEEN 1 AND 3 FROM t", 6))
	want := [][]any{{int64(1), false}, {int64(2), true}, {int64(3), true}, {int64(4), true}, {int64(5), false}, {int64(6), nil}}
	fixture.ScalarStep(t, p, fixture.Select("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	fixture.ScalarStep(t, p, fixture.Prepared(fixture.Exec("UPDATE result SET b=id BETWEEN 2 AND 4 WHERE id BETWEEN 2 AND 4", 3)))
	fixture.ScalarStep(t, p, fixture.Select("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		q string
		d fixture.Diagnostic
	}{
		{"UPDATE result SET b=id BETWEEN 1 AND TRUE", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer <= boolean", Hint: hint, Position: "24"}},
		{"UPDATE result SET b=id BETWEEN 7 AND 'oops'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "38"}},
	} {
		step := fixture.ImplicitError(tc.q, tc.d)
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
		fixture.ScalarStep(t, p, fixture.Select("SELECT id,b FROM result ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
	}
	group := fixture.Select("SELECT v BETWEEN 1 AND 3,count(*) FROM t GROUP BY v BETWEEN 1 AND 3", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(2)}, {true, int64(3)}, {nil, int64(1)}})
	group.Bag = true
	betweenReferenceBoth(t, p, group)
	betweenReferenceBoth(t, p, fixture.Select("SELECT count(*) FROM t GROUP BY v BETWEEN 1 AND 3 HAVING v BETWEEN 1 AND 3", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN 1 AND 3),s BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),n BOOL DEFAULT (2 NOT BETWEEN 1 AND 3))", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO defaults DEFAULT VALUES", 1))
	fixture.ScalarStep(t, p, fixture.Select("SELECT * FROM defaults", []string{"b", "s", "n"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
	fixture.ScalarStep(t, p, fixture.Exec("DELETE FROM t WHERE v BETWEEN SYMMETRIC 3 AND 1 = TRUE", 3))
	fixture.ScalarStep(t, p, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(6)}}))
	fixture.ScalarStep(t, p, fixture.Prepared(fixture.Exec("DELETE FROM t WHERE v NOT BETWEEN 1 AND 3", 2)))
	fixture.ScalarStep(t, p, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
}
