package stuntgres_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityReviewCascadeNotice(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			var notices []stuntgres.Error
			db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(notice stuntgres.Error) { notices = append(notices, notice) }))
			defer db.Close()
			db.SetMaxOpenConns(1)
			execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,9,11)")
			execSQL(t, db, "CREATE SEQUENCE owned OWNED BY t.b")
			execSQL(t, db, "CREATE VIEW v AS SELECT b,c FROM t")
			execSQL(t, db, "CREATE VIEW w AS SELECT * FROM v")
			if len(notices) != 0 {
				t.Fatal("unexpected setup notice", notices)
			}
			transactionAffected(t, t.Context(), db, "ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", prepared, 0)
			want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to view v\ndrop cascades to view w"}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
		})
	}
}
