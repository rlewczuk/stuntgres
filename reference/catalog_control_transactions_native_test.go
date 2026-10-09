package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTransactionNativeControls(t *testing.T) {
	for _, action := range []string{"reset_execution", "reset_commit", "restore", "failed_restore", "reader_error", "canceled_restore"} {
		t.Run(action, func(t *testing.T) {
			p := newReferenceScenario(t)
			catalogControlSQLWitness(t, p)
			db, a, b := catalogControlConnections(t, p, false)
			ctx := context.Background()
			catalogControlAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			catalogControlAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			catalogControlAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", false, 1)
			var dump bytes.Buffer
			if err = stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "VALUES (77)") || strings.Contains(dump.String(), "VALUES (1)") {
				t.Fatal("dump leaked private rows", dump.String())
			}
			switch action {
			case "reset_execution", "reset_commit":
				if err = stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if action == "reset_commit" {
					transactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
				} else {
					_, err = tx.QueryContext(ctx, "SELECT v FROM t")
					transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				catalogControlAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				catalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false)
			case "restore":
				if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99)")); err != nil {
					t.Fatal(err)
				}
				transactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
				catalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 99)
			default:
				if action == "failed_restore" {
					err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO missing VALUES(99)"))
					referenceState(t, err, "42P01")
				} else if action == "reader_error" {
					failure := errors.New("transaction reader failure")
					err = stuntgres.Restore(ctx, db, catalogControlFailingReader{failure})
					if !errors.Is(err, failure) {
						t.Fatal(err)
					}
				} else {
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					err = stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE t(v INT)"))
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
				catalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 77)
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				catalogControlSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
			}
		})
	}
}
