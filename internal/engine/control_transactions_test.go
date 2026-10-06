package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestControlTransactionsEndMatrix(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		for _, writer := range []bool{false, true} {
			for _, state := range []string{"active", "stale_failure", "sql_failure"} {
				for _, end := range []string{"COMMIT", "END AND NO CHAIN", "COMMIT AND CHAIN", "ROLLBACK", "ABORT AND CHAIN"} {
					t.Run(replacement+"/"+map[bool]string{false: "reader", true: "writer"}[writer]+"/"+state+"/"+end, func(t *testing.T) {
						e := New()
						a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
						token := a.token
						sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
						sessionSQL(t, b, "INSERT INTO t VALUES(77)")
						controlsRequire(t, a, "BEGIN")
						if writer {
							sessionSQL(t, a, "INSERT INTO t VALUES(1)")
						}
						old := a.transactions.ref
						if state == "sql_failure" {
							controlsAbort(t, a)
						}
						var err error
						if replacement == "reset" {
							err = e.Reset(context.Background())
						} else {
							err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);"))
						}
						if err != nil {
							t.Fatal(err)
						}
						frontier := e.commitSeq
						if state == "stale_failure" {
							for range 2 {
								op := transactionOperation(t, a)
								_, err = e.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
								op.Done()
								if !errors.Is(err, ErrCatalogReplaced) {
									t.Fatal(err)
								}
							}
						}
						err = controlsExecute(t, a, end)
						commit := strings.HasPrefix(end, "COMMIT") || strings.HasPrefix(end, "END")
						if commit {
							if !errors.Is(err, ErrCatalogReplaced) {
								t.Fatal("stale commit", err)
							}
						} else if err != nil {
							t.Fatal(err)
						}
						chain := !commit && strings.HasSuffix(end, "AND CHAIN")
						if a.token != token || a.transactions.writes != nil || e.commitSeq != frontier {
							t.Fatal("replacement/end mutated identity/private writes/frontier")
						}
						if chain {
							if a.TransactionState() != TxActive || a.transactions.ref.epoch != e.CatalogRead().Epoch() || a.transactions.ref.ID() <= old.ID() {
								t.Fatal("rollback chain not current")
							}
							controlsRequire(t, a, "ROLLBACK")
						} else if a.TransactionState() != TxIdle || a.transactions.ref != (TxRef{}) || a.transactions.ticket != nil {
							t.Fatal("end retained owner/installed commit chain")
						}
						lockEmpty(t, e)
						if replacement == "restore" {
							transactionRows(t, b, "SELECT v FROM t", 99)
						}
					})
				}
			}
		}
	}
}

// Foreign replacement checks context at admission, under the target lock, and
// finally after remapping/ACL preparation. Cancel at that last check, either
// before its returned error or immediately after the successful nil observation.
type controlReplacementContext struct {
	context.Context
	cancel context.CancelFunc
	late   bool
	checks int
}

func (c *controlReplacementContext) Err() error {
	c.checks++
	if c.checks == 3 {
		if c.late {
			err := c.Context.Err()
			c.cancel()
			return err
		}
		c.cancel()
	}
	return c.Context.Err()
}

func TestControlTransactionsReplacementCancellationBoundary(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_install", true: "after_install"}[late], func(t *testing.T) {
			source, target := New(), New()
			run(t, source, "CREATE TABLE t(v INT NOT NULL)")
			run(t, source, "INSERT INTO t VALUES(99)")
			a, b := sessionStart(t, target, nil), sessionStart(t, target, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			sessionSQL(t, b, "INSERT INTO t VALUES(77)")
			retained := transactionRows(t, b, "SELECT v FROM t", 77)
			defer retained.Close()
			before := target.CatalogRead()
			ref := catalogRef(t, target, before, "t")
			owner, storage, frontier := target.owner, target.storage[ref.Address], target.commitSeq
			stage := target.BeginCatalogStage(before)
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			boundary := &controlReplacementContext{Context: ctx, cancel: cancel, late: late}
			changes, err := target.ReplaceCatalog(boundary, source.CatalogRead())
			if ctx.Err() != context.Canceled || boundary.checks != 3 || target.owner != owner {
				t.Fatal("boundary not exercised", boundary.checks, err)
			}
			if late {
				if err != nil || !changes.Replacement || target.CatalogRead().Epoch() != before.Epoch()+1 || target.commitSeq != frontier+1 {
					t.Fatal("late cancellation retracted install", changes, err)
				}
				transactionRows(t, b, "SELECT v FROM t", 99)
				if !errors.Is(target.ValidateObjectRef(target.CatalogRead(), ref), ErrCatalogReplaced) {
					t.Fatal("old ref eligible")
				}
				prepared, err := target.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = target.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("old stage publishable", err)
				}
				op := transactionOperation(t, a)
				_, err = target.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
				op.Done()
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if err = controlsExecute(t, a, "COMMIT"); !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, context.Canceled) || changes.Replacement || len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 {
					t.Fatal(changes, err)
				}
				if target.CatalogRead().state != before.state || target.storage[ref.Address] != storage || target.commitSeq != frontier {
					t.Fatal("failed replacement changed state/storage/frontier")
				}
				if err = target.ValidateObjectRef(before, ref); err != nil {
					t.Fatal(err)
				}
				prepared, err := target.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = target.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); err != nil {
					t.Fatal("eligible stage fenced", err)
				}
				controlsRequire(t, a, "COMMIT")
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 77)
			}
			if retained.count != 1 || retained.source.view.read().catalog.state != before.state || testExport(storage.versions[0].values[0]) != int64(77) {
				t.Fatal("retained generation changed")
			}
		})
	}
}
