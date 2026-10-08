package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

// Expected values come from Main's pinned PostgreSQL 18 same-sequence probes.
func TestIdentityLinkedSequenceSourceOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{
			`INSERT INTO i OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),10),(nextval('i_id_seq'),11)`,
			`INSERT INTO i OVERRIDING USER VALUE SELECT nextval('i_id_seq'),p FROM source`,
		} {
			t.Run(fmt.Sprintf("%t/%s", prepared, query), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, false, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0)
				tableCoreExec(t, db, false, `CREATE TABLE source(p INT)`, 0)
				tableCoreExec(t, db, false, `INSERT INTO source VALUES(10),(11)`, 2)
				tableCoreExec(t, db, prepared, query, 2)
				wantTuples(t, db, `SELECT * FROM i ORDER BY id`, [][]any{{int64(2), int64(10)}, {int64(4), int64(11)}})
				wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(4), true}})
			})
		}
	}
}

func TestIdentitySingleDestinationProjectionOrder(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, create, insert string
			want                 [][]any
		}{
			{"payload_before_identity", `CREATE TABLE i(p INT,id INT GENERATED ALWAYS AS IDENTITY)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`, [][]any{{int64(1), int64(2)}}},
			{"reordered_targets", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),99)`, [][]any{{int64(1), int64(2)}}},
			{"identity_before_payload", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`, [][]any{{int64(1), int64(2)}}},
		} {
			t.Run(fmt.Sprintf("%t/%s", prepared, tc.name), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, false, tc.create, 0)
				tableCoreExec(t, db, prepared, tc.insert, 1)
				wantTuples(t, db, `SELECT * FROM i`, tc.want)
				wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(2), true}})
			})
		}
	}
}
