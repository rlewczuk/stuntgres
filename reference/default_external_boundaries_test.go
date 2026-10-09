//go:build reference

package reference_test

import "testing"

func TestReferenceDefaultExternalVolatileBoundaries(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	// The minimum fixed-nextval DEFAULT surface also supports native replay.
	run(referenceExec(`CREATE SEQUENCE default_seq`, 0))
	run(referenceExec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0))
	run(referenceExec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3))
	run(referenceExec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1))
	run(referenceSelect(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
	nativeRecord := &referenceRecorder{}
	for _, step := range []referenceStep{
		referenceExec(`CREATE SEQUENCE default_seq`, 0),
		referenceExec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0),
		referenceExec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3),
		referenceExec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1),
		referenceSelect(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}),
	} {
		referenceRunStep(t, p.ctx, p.memory, nativeRecord, step)
	}
}
