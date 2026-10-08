package reference_test

import "testing"

func TestReferenceFixtureNativeIsolation(t *testing.T) {
	// Equal test names must not retain an earlier pilot's process-local engine.
	first := newReferenceFixture(t, false, true)
	first.step(t, referenceExec("CREATE TABLE isolated(v INT)", 0))
	first.step(t, referenceExec("INSERT INTO isolated VALUES (7)", 1))
	var value int64
	if err := first.native.QueryRowContext(first.ctx, "SELECT v FROM isolated").Scan(&value); err != nil || value != 7 {
		t.Fatalf("native control pool shares wrapped state: value=%d error=%v", value, err)
	}
	second := newReferenceFixture(t, false, true)
	second.step(t, referenceExec("CREATE TABLE isolated(v INT)", 0))
	second.step(t, referenceSelect("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, nil))
	first.step(t, referenceSelect("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
