package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres"
)

func TestCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed=%t/prepared=%t", mixed, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE SEQUENCE s")
				detail := "constraint c1 on table t depends on sequence s\nconstraint c2 on table t depends on sequence s"
				if mixed {
					execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT c1 CHECK(nextval('s')>0))")
					execSQL(t, db, "CREATE VIEW v AS SELECT nextval('s') AS n")
					detail = "constraint c1 on table t depends on sequence s\nview v depends on sequence s"
				} else {
					execSQL(t, db, "CREATE TABLE t(a INT,CONSTRAINT c1 CHECK(nextval('s')>0),CONSTRAINT c2 CHECK(nextval('s')>0))")
				}
				execSQL(t, db, "INSERT INTO t VALUES(1)")
				var before bytes.Buffer
				if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
					t.Fatal(err)
				}
				checkExecError(t, db, prepared, "DROP SEQUENCE s", stuntgres.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				var after bytes.Buffer
				if err := stuntgres.Dump(context.Background(), db, &after); err != nil || before.String() != after.String() {
					t.Fatal("RESTRICT changed catalog/data/provider state", err)
				}
				execSQL(t, db, "INSERT INTO t VALUES(2)")
				wantTuples(t, db, "SELECT a FROM t ORDER BY a", [][]any{{int64(1)}, {int64(2)}})
				if mixed {
					wantTuples(t, db, "SELECT n FROM v", [][]any{{int64(3)}})
				}
			})
		}
	}
}

func TestCheckReviewDependencyPrivateDropBlocksCreate(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if _, err = a.ExecContext(ctx, "CREATE SEQUENCE s"); err != nil {
				t.Fatal(err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, "DROP SEQUENCE s CASCADE"); err != nil {
				t.Fatal(err)
			}
			// Public native APIs do not expose queues. A bounded cancellation is
			// the incumbent behavioral lock witness, not a sleep or guessed code.
			blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
			r, createErr := b.ExecContext(blocked, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))")
			stop()
			if r != nil || !errors.Is(createErr, context.DeadlineExceeded) {
				t.Fatalf("private provider deletion must block CREATE: result=%v error=%v", r, createErr)
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				if r, err = b.ExecContext(ctx, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"); err == nil || r != nil {
					t.Fatal("missing provider accepted", r, err)
				}
				if _, err = b.ExecContext(ctx, "CREATE TABLE newly_dependent(a INT CHECK(a=1))"); err != nil {
					t.Fatal("failed CREATE leaked publication", err)
				}
			} else {
				if _, err = b.ExecContext(ctx, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = b.ExecContext(ctx, "INSERT INTO newly_dependent VALUES(1)"); err != nil {
				t.Fatal("surviving definition unusable", err)
			}
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal("orphan catalog", err)
			}
		})
	}
}

func TestCheckReviewDependencyRestrictReversedDeclaration(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE SEQUENCE s")
	execSQL(t, db, "CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))")
	for _, prepared := range []bool{false, true} {
		checkExecError(t, db, prepared, "DROP SEQUENCE s", stuntgres.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	}
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	wantTuples(t, db, "SELECT a FROM t", [][]any{{int64(1)}})
}
