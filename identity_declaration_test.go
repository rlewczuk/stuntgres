package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestIdentityDeclaration(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, `CREATE TABLE i(id INTEGER GENERATED ALWAYS AS IDENTITY, p INTEGER DEFAULT 7)`, 0)
			tableCoreMetadata(t, db, prepared, `SELECT * FROM i`, []string{"id", "p"}, []string{"INT4", "INT4"}, []bool{false, true})
			tableCoreExec(t, db, prepared, `INSERT INTO i(p) VALUES(DEFAULT)`, 1)
			tableCoreExec(t, db, prepared, `INSERT INTO i VALUES(DEFAULT,DEFAULT)`, 1)
			wantTuples(t, db, `SELECT * FROM i ORDER BY id`, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}})
		})
	}
}
