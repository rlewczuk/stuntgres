//go:build reference

package reference_test

import "testing"

func TestReferenceSerialRollbackAndRename(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep { return []referenceStep{
		referenceExec(`CREATE TABLE t(id SERIAL)`,0),
		referenceExec(`BEGIN`,0),
		referenceExec(`INSERT INTO t DEFAULT VALUES`,1),
		referenceExec(`ROLLBACK`,0),
		referenceSelect(`SELECT * FROM t`,[]string{"id"},[]string{"INT4"},[][]any{}),
		referenceSelect(`SELECT currval('t_id_seq')`,[]string{"currval"},[]string{"INT8"},[][]any{{int64(1)}}),
		referenceExec(`ALTER SEQUENCE t_id_seq RENAME TO renamed`,0),
		referenceExec(`CREATE SEQUENCE t_id_seq START 100`,0),
		referenceExec(`INSERT INTO t DEFAULT VALUES`,1),
		referenceSelect(`SELECT * FROM t`,[]string{"id"},[]string{"INT4"},[][]any{{int64(2)}}),
		referenceSelect(`SELECT nextval('t_id_seq')`,[]string{"nextval"},[]string{"INT8"},[][]any{{int64(100)}}),
		referenceExec(`ALTER TABLE t RENAME COLUMN id TO renamed_id`,0),
		referenceExec(`ALTER TABLE t RENAME TO renamed_table`,0),
		referenceExec(`INSERT INTO renamed_table DEFAULT VALUES`,1),
		referenceSelect(`SELECT * FROM renamed_table ORDER BY renamed_id`,[]string{"renamed_id"},[]string{"INT4"},[][]any{{int64(2)},{int64(3)}}),
	} })
}

func TestReferenceSerialCandidateCheck(t *testing.T) {
	serialReferenceStreams(t,func(bool) []referenceStep { return []referenceStep{
		referenceExec(`CREATE TABLE t(id SERIAL,CHECK(nextval('t_id_seq')>0))`,0),
		referenceSelect(`SELECT last_value,is_called FROM t_id_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}),
		referenceExec(`INSERT INTO t DEFAULT VALUES`,1),
		referenceSelect(`SELECT * FROM t`,[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}),
		referenceSelect(`SELECT currval('t_id_seq')`,[]string{"currval"},[]string{"INT8"},[][]any{{int64(2)}}),
		referenceExec(`DROP TABLE t RESTRICT`,0),
		referenceExec(`CREATE SEQUENCE t_id_seq`,0),
	} })
}
