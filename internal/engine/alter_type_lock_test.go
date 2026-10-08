package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAlterTypeGrantTimeLiveRows(t *testing.T) {
	for _, kind := range []string{"late-invalid", "replace-invalid", "delete-invalid", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(c TEXT)")
			seed := "'oops'"
			if kind == "late-invalid" || kind == "cancel" {
				seed = "'7'"
			}
			sessionSQL(t, a, "INSERT INTO t VALUES("+seed+")")
			ref := roleTableRef(t, e, "t")
			old, storage := e.state.descriptors[ref.Address], e.storage[ref.Address]
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, b, ctx, "ALTER TABLE t ALTER c TYPE INT USING c::int", false)
			lockPending(t, e, 1)
			if kind == "cancel" {
				cancel()
				lockAwait(t, done, context.Canceled)
				if e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage {
					t.Fatal("canceled waiter changed state")
				}
				transactionEnd(t, a, tx, false)
			} else {
				switch kind {
				case "late-invalid":
					sessionSQL(t, a, "INSERT INTO t VALUES('oops')")
				case "replace-invalid":
					sessionSQL(t, a, "UPDATE t SET c='7'")
				case "delete-invalid":
					sessionSQL(t, a, "DELETE FROM t")
				}
				transactionEnd(t, a, tx, true)
				if kind == "late-invalid" {
					select {
					case err := <-done:
						state(t, err, "22P02")
					case <-time.After(5 * time.Second):
						t.Fatal("TYPE did not terminate after AX grant")
					}
					if e.state.descriptors[ref.Address] != old {
						t.Fatal("published invalid grant-time conversion")
					}
				} else {
					lockAwait(t, done, nil)
				}
			}
			lockEmpty(t, e)
			if kind == "replace-invalid" {
				if got := tuples(sessionSQL(t, b, "SELECT c FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal(got)
				}
			}
			if kind == "delete-invalid" {
				result := sessionSQL(t, b, "SELECT c FROM t")
				if len(tuples(result)) != 0 || result.Rows.Schema()[0].Usage.Type.ID() != 23 {
					t.Fatal(result.Rows.Schema())
				}
			}
		})
	}
}

func TestAlterTypeCancellationDuringConversion(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(c TEXT)")
	run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(strings.Repeat("('7'),", 128), ","))
	ref := roleTableRef(t, e, "t")
	old, storage := e.state.descriptors[ref.Address], e.storage[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	op.ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 25}
	_, err = e.Execute(op, statement(t, "ALTER TABLE t ALTER c TYPE INT USING c::int"), 0, nil)
	if !errors.Is(err, context.Canceled) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage {
		t.Fatal(err, "published partial rewrite")
	}
}
