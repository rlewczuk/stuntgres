//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultExternalVolatileBoundaries(t *testing.T) {
	p := fixture.NewPair(t)
	record := &fixture.Recorder{}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, record, s) }
	// The minimum fixed-nextval DEFAULT surface also supports native replay.
	run(fixture.Exec(`CREATE SEQUENCE default_seq`, 0))
	run(fixture.Exec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0))
	run(fixture.Exec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3))
	run(fixture.Exec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1))
	run(fixture.Select(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}))
	nativeRecord := &fixture.Recorder{}
	for _, step := range []fixture.Step{
		fixture.Exec(`CREATE SEQUENCE default_seq`, 0),
		fixture.Exec(`CREATE TABLE volatile_d(id INT,v BIGINT DEFAULT nextval('default_seq'))`, 0),
		fixture.Exec(`INSERT INTO volatile_d(id) VALUES(1),(2),(3)`, 3),
		fixture.Exec(`INSERT INTO volatile_d VALUES(4,DEFAULT)`, 1),
		fixture.Select(`SELECT * FROM volatile_d ORDER BY id`, []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}),
	} {
		fixture.RunStep(t, p.Ctx, p.Memory, nativeRecord, step)
	}
}
