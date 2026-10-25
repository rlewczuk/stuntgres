package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestAlterTypeGrantTimeLiveRows(t *testing.T) {
	for _, kind := range []string{"late-invalid", "replace-invalid", "delete-invalid", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(c TEXT)")
			seed := "'oops'"
			if kind == "late-invalid" || kind == "cancel" {
				seed = "'7'"
			}
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES("+seed+")")
			ref := enginetest.RoleTableRef(t, e, "t")
			old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "ALTER TABLE t ALTER c TYPE INT USING c::int", false)
			enginetest.LockPending(t, e, 1)
			if kind == "cancel" {
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
					t.Fatal("canceled waiter changed state")
				}
				enginetest.TransactionEnd(t, a, tx, false)
			} else {
				switch kind {
				case "late-invalid":
					enginetest.SessionSQL(t, a, "INSERT INTO t VALUES('oops')")
				case "replace-invalid":
					enginetest.SessionSQL(t, a, "UPDATE t SET c='7'")
				case "delete-invalid":
					enginetest.SessionSQL(t, a, "DELETE FROM t")
				}
				enginetest.TransactionEnd(t, a, tx, true)
				if kind == "late-invalid" {
					select {
					case err := <-done:
						enginetest.State(t, err, "22P02")
					case <-time.After(5 * time.Second):
						t.Fatal("TYPE did not terminate after AX grant")
					}
					if e.Catalog.State.Get(ref.Address) != old {
						t.Fatal("published invalid grant-time conversion")
					}
				} else {
					enginetest.LockAwait(t, done, nil)
				}
			}
			enginetest.LockEmpty(t, e)
			if kind == "replace-invalid" {
				if got := enginetest.Tuples(enginetest.SessionSQL(t, b, "SELECT c FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal(got)
				}
			}
			if kind == "delete-invalid" {
				result := enginetest.SessionSQL(t, b, "SELECT c FROM t")
				if len(enginetest.Tuples(result)) != 0 || result.Rows.Schema()[0].Usage.Type.ID() != 23 {
					t.Fatal(result.Rows.Schema())
				}
			}
		})
	}
}

func TestAlterTypeCancellationDuringConversion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(c TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES"+strings.TrimSuffix(strings.Repeat("('7'),", 128), ","))
	ref := enginetest.RoleTableRef(t, e, "t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, cleanup, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	op.Ctx = &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 25}
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t ALTER c TYPE INT USING c::int"), 0, nil)
	if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
		t.Fatal(err, "published partial rewrite")
	}
}

func TestAlterTypeOuterTransactionAXPublication(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(c INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "ALTER TABLE t ALTER c TYPE BIGINT USING c+1")
			type outcome struct {
				result core.Result
				err    error
			}
			done := make(chan outcome, 1)
			query := enginetest.Statement(t, "SELECT c FROM t")
			go func() {
				op, err := b.Acquire(context.Background())
				if err != nil {
					done <- outcome{err: err}
					return
				}
				defer op.Done()
				result, err := e.Execute(op, query, 0, nil)
				done <- outcome{result, err}
			}()
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, commit)
			var result core.Result
			select {
			case out := <-done:
				if out.err != nil {
					t.Fatal(out.err)
				}
				result = out.result
			case <-time.After(5 * time.Second):
				t.Fatal("peer SELECT did not terminate after outer transaction")
			}
			defer result.Rows.Close()
			want, id := int64(7), uint32(23)
			if commit {
				want, id = 8, 20
			}
			if uint32(result.Rows.Schema()[0].Usage.Type.ID()) != id || !reflect.DeepEqual(enginetest.Tuples(result), [][]driver.Value{{want}}) {
				t.Fatal("schema/data not published together", result.Rows.Schema())
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
