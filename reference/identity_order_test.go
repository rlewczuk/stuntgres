//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceIdentityLinkedSequenceSourceOrder(t *testing.T) {
	for _, query := range []string{
		`INSERT INTO i OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),10),(nextval('i_id_seq'),11)`,
		`INSERT INTO i OVERRIDING USER VALUE SELECT nextval('i_id_seq'),p FROM source`,
	} {
		t.Run(query, func(t *testing.T) {
			identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
				identityRefExec(t, db, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, 0)
				identityRefExec(t, db, `CREATE TABLE source(p INT)`, 0)
				identityRefExec(t, db, `INSERT INTO source VALUES(10),(11)`, 2)
				held, err := db.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				result, err := held.Exec()
				if err != nil {
					t.Fatal(err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 2 {
					t.Fatal(n, err)
				}
				identityRefRows(t, db, `SELECT * FROM i ORDER BY id`, [][]int64{{2, 10}, {4, 11}})
				identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{4}})
			})
		})
	}
}

func TestReferenceIdentitySingleDestinationProjectionOrder(t *testing.T) {
	for _, tc := range []struct{ name, create, insert string }{
		{"payload_before_identity", `CREATE TABLE i(p INT,id INT GENERATED ALWAYS AS IDENTITY)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`},
		{"reordered_targets", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p,id) OVERRIDING USER VALUE VALUES(nextval('i_id_seq'),99)`},
		{"identity_before_payload", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,p INT)`, `INSERT INTO i(p) VALUES(nextval('i_id_seq'))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
				identityRefExec(t, db, tc.create, 0)
				identityRefExec(t, db, tc.insert, 1)
				identityRefRows(t, db, `SELECT * FROM i`, [][]int64{{1, 2}})
				identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{2}})
			})
		})
	}
}
