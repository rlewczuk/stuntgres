package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterNullabilityReviewDependencyTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, throughSequence := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t/commit=%t/sequence=%t", prepared, commit, throughSequence), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					ctx := t.Context()
					execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT)")
					execSQL(t, db, "INSERT INTO t VALUES(7,9,11)")
					execSQL(t, db, "CREATE SEQUENCE owned OWNED BY t.b")
					definition := "CREATE VIEW v AS SELECT b,c FROM t"
					if throughSequence {
						definition = "CREATE VIEW v AS SELECT c,last_value FROM t,owned"
					}
					execSQL(t, db, definition)
					execSQL(t, db, "CREATE VIEW w AS SELECT * FROM v")
					tx, err := db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					transactionAffected(t, ctx, tx, "ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", prepared, 0)
					transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(8)", prepared, 1)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						alterDropMetadata(t, db, false, "SELECT * FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{false}, 2)
						wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(7)}, {int64(8)}})
						transactionAffected(t, ctx, db, "CREATE SEQUENCE owned", false, 0)
						transactionAffected(t, ctx, db, "CREATE VIEW v AS SELECT a FROM t", false, 0)
						transactionAffected(t, ctx, db, "CREATE VIEW w AS SELECT * FROM v", false, 0)
					} else {
						wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7), int64(9), int64(11)}})
						expected := [][]any{{int64(9), int64(11)}}
						if throughSequence {
							expected = [][]any{{int64(11), int64(1)}}
						}
						wantTuples(t, db, "SELECT * FROM w", expected)
					}
				})
			}
		}
	}
}

func TestAlterNullabilityReviewRemainingDependency(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT,b INT,c INT)")
			execSQL(t, db, "INSERT INTO t VALUES(7,9,11)")
			execSQL(t, db, "CREATE SEQUENCE owned OWNED BY t.b")
			execSQL(t, db, "CREATE VIEW v AS SELECT b,c FROM t")
			execSQL(t, db, "CREATE VIEW w AS SELECT * FROM v")
			execSQL(t, db, "CREATE VIEW z AS SELECT c FROM t")
			_, err := transactionExec(t.Context(), db, "ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", prepared)
			notNullDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop column c of table t because other objects depend on it", Detail: "view z depends on column c of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			wantTuples(t, db, "SELECT * FROM w", [][]any{{int64(9), int64(11)}})
			wantTuples(t, db, "SELECT * FROM z", [][]any{{int64(11)}})
			wantTuples(t, db, "SELECT nextval('owned')", [][]any{{int64(1)}})
			alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, []bool{true, true, true}, 1)
		})
	}
}
