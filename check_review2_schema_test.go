package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres"
)

func TestCheckReview2SchemaPrivateDropCancellation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit=%t/prepared=%t", commit, prepared), func(t *testing.T) {
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
				if _, err = a.ExecContext(ctx, "CREATE SEQUENCE public.s"); err != nil {
					t.Fatal(err)
				}
				var before bytes.Buffer
				if err = stuntgres.Dump(ctx, db, &before); err != nil {
					t.Fatal(err)
				}
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(ctx, "DROP SEQUENCE public.s CASCADE"); err != nil {
					t.Fatal(err)
				}
				const create = "CREATE SCHEMA checked CREATE TABLE earlier(a INT CHECK(a=1)) CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('public.s')>0))"
				var stmt *sql.Stmt
				if prepared {
					stmt, err = b.PrepareContext(ctx, create)
					if err != nil {
						t.Fatal("Prepare must be effect-free and not wait on private DROP", err)
					}
					defer stmt.Close()
				}
				createExec := func(callCtx context.Context) (sql.Result, error) {
					if stmt != nil {
						return stmt.ExecContext(callCtx)
					}
					return b.ExecContext(callCtx, create)
				}
				// This is the existing public/native deadline witness, not a sleep:
				// queues are intentionally private. Exact AS/AX admission is asserted
				// internally. PostgreSQL's observed nonwaiting orphan interval is NOT
				// a parity expectation; native creation deliberately waits safely.
				blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
				result, createErr := createExec(blocked)
				stop()
				if result != nil || !errors.Is(createErr, context.DeadlineExceeded) {
					t.Fatalf("inline CREATE must wait behind private provider DROP: result=%v error=%v", result, createErr)
				}
				var after bytes.Buffer
				if err = stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before.String() {
					t.Fatal("cancellation published schema/table fragments or changed provider state", err)
				}
				// Assert namespace absence directly as well as the whole dump: an
				// earlier successfully staged inline table must not escape either.
				result, err = b.ExecContext(ctx, "ALTER SCHEMA checked RENAME TO leaked")
				if result != nil {
					t.Fatal("canceled CREATE left a schema", result)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "3F000", Message: `schema "checked" does not exist`})
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal("canceled inline CREATE interfered with deleting transaction outcome", err)
				}
				if commit {
					result, err = createExec(ctx)
					if result != nil || err == nil {
						t.Fatal("inline CREATE accepted missing committed-away provider", result, err)
					}
					// Reusing the same schema name proves failed retry did not leave
					// even an empty namespace. Reusing the retained statement with a
					// new provider proves it does not retain canceled execution state.
					if _, err = b.ExecContext(ctx, "CREATE SEQUENCE public.s"); err != nil {
						t.Fatal("DROP commit did not remove provider", err)
					}
				}
				result, err = createExec(ctx)
				if err != nil || result == nil {
					t.Fatal("healthy later inline CREATE failed", result, err)
				}
				if count, err := result.RowsAffected(); err != nil || count != 0 {
					t.Fatal("inline CREATE affected count", count, err)
				}
				for _, q := range []string{"INSERT INTO checked.earlier VALUES(1)", "INSERT INTO checked.t VALUES(7)"} {
					result, err = b.ExecContext(ctx, q)
					if err != nil {
						t.Fatal("surviving definition unusable", q, err)
					}
					if count, err := result.RowsAffected(); err != nil || count != 1 {
						t.Fatal(q, count, err)
					}
				}
				wantTuples(t, db, "SELECT a FROM checked.earlier", [][]any{{int64(1)}})
				wantTuples(t, db, "SELECT a FROM checked.t", [][]any{{int64(7)}})
				var current int64
				if err = b.QueryRowContext(ctx, "SELECT currval('public.s')").Scan(&current); err != nil || current != 1 {
					t.Fatal("CREATE/cancellation evaluated CHECK or provider was unusable", current, err)
				}
				var dump bytes.Buffer
				if err = stuntgres.Dump(ctx, db, &dump); err != nil {
					t.Fatal("orphan catalog after inline CREATE", err)
				}
			})
		}
	}
}
