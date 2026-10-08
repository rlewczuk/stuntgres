package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestViewReplaceReadOnly(t *testing.T) {
	for _, tc := range []struct{ query, tag string }{{"CREATE OR REPLACE VIEW absent AS SELECT id FROM missing", "CREATE VIEW"}, {"ALTER VIEW absent RENAME TO renamed", "ALTER VIEW"}, {"DROP VIEW absent", "DROP VIEW"}} {
		t.Run(tc.tag, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), "BEGIN READ ONLY"); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			_, err = conn.ExecContext(context.Background(), tc.query)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
		})
	}
}

func TestViewReplaceHeldRows(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
		execSQL(t, db, q)
	}
	rows, err := db.QueryContext(ctx, "SELECT id FROM v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	execSQL(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
	execSQL(t, db, "DROP VIEW v")
	queryCoreRows(t, rows, nil, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
}
