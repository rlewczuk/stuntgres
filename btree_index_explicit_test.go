package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestBtreeIndexExplicitAdmissionRegression(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			for _, target := range []string{"missing", "denied", "writer-blocked"} {
				label := map[bool]string{false: "writable", true: "read-only"}[readOnly] + "/" + map[bool]string{false: "direct", true: "held-prepared"}[prepared] + "/" + target
				t.Run(label, func(t *testing.T) {
					ctx := context.Background()
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					execSQL(t, db, "CREATE TABLE t(n INT)")
					execSQL(t, db, "INSERT INTO t VALUES(1)")
					writer, builder := sequenceConn(t, db), sequenceConn(t, db)
					if target == "denied" {
						sequenceExec(t, builder, "CREATE USER reader")
						sequenceExec(t, builder, "SET ROLE reader")
					}
					if target == "writer-blocked" {
						tx, err := writer.BeginTx(ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						if _, err := tx.ExecContext(ctx, "LOCK TABLE t IN ROW EXCLUSIVE MODE"); err != nil {
							t.Fatal(err)
						}
					}
					sequenceExec(t, builder, "SET lock_timeout='20ms'")
					query := "CREATE INDEX idx ON t(n)"
					if target == "missing" {
						query = "CREATE INDEX idx ON missing(n)"
					}
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = builder.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal("syntax-only held Prepare", err)
						}
						defer held.Close()
					}
					tx, err := builder.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if prepared {
						active := tx.StmtContext(ctx, held)
						_, err = active.ExecContext(ctx)
						active.Close()
					} else {
						_, err = tx.ExecContext(ctx, query)
					}
					want := stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
					if readOnly {
						want = stuntgres.Error{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}
					}
					typeCoreDiagnostic(t, err, want)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					wantTuples(t, db, "SELECT relname FROM pg_class WHERE relname='idx'", [][]any{})
					wantTuples(t, db, "SELECT n FROM t", [][]any{{int64(1)}})
				})
			}
		}
	}
}
