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
							if a.TransactionState() != TxActive || a.transactions.ref.Epoch() != e.CatalogRead().Epoch() || a.transactions.ref.ID() <= old.ID() {
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

// This context is used only at the real prepared-generation install seam.
// Cancellation either precedes its final observation or follows a successful
// observation, without depending on how many checks preparation performs.
type controlReplacementContext struct {
	context.Context
	cancel   context.CancelFunc
	late     bool
	observed bool
}

func (c *controlReplacementContext) Err() error {
	c.observed = true
	if c.late {
		err := c.Context.Err()
		c.cancel()
		return err
	}
	c.cancel()
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
			owner, storage, frontier := target.domain, target.storage[ref.Address], target.commitSeq
			stage := target.BeginCatalogStage(before)
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			boundary := &controlReplacementContext{Context: ctx, cancel: cancel, late: late}
			// Full ReplaceCatalog behavior is covered by the catalog replacement
			// tests. Prepare a genuine foreign generation here so cancellation
			// targets installation, not admission or the live-row scan.
			replacement := source.CatalogRead()
			sourceRoles, targetRoles := source.Roles(), target.Roles()
			preparedStorage := make(map[ObjectAddress]*relationStorage)
			source.mu.Lock()
			for address, relation := range source.storage {
				preparedStorage[address] = &relationStorage{versions: append([]rowVersion(nil), relation.versions...)}
			}
			preparedSequences, sequenceErr := copySequenceState(source, replacement)
			indexes, indexErr := captureIndexSnapshot(context.Background(), replacement, source.storage, source.indexRoots, source.commitSeq)
			source.mu.Unlock()
			if sequenceErr != nil {
				t.Fatal(sequenceErr)
			}
			if indexErr != nil {
				t.Fatal(indexErr)
			}
			err := func() error {
				target.replacementMu.Lock()
				defer target.replacementMu.Unlock()
				target.mu.Lock()
				defer target.mu.Unlock()
				next, err := target.remapPreparedCatalog(cloneCatalog(replacement.state), sourceRoles, targetRoles, nil)
				if err != nil {
					return err
				}
				nextRevision := target.nextRevision
				for _, descriptor := range next.descriptors {
					if err := validateACL(target.roleRead(CatalogRead{target, next}, target.roles), descriptor); err != nil {
						return err
					}
					if revision := descriptor.Revision(); revision >= nextRevision {
						nextRevision = revision + 1
					}
				}
				prepared, err := target.prepareForeignIndexStorage(ctx, next, preparedStorage, indexes)
				if err != nil {
					return err
				}
				return target.installForeignCatalogLocked(boundary, next, preparedStorage, preparedSequences, nextRevision, prepared)
			}()
			if err == nil {
				target.locks.fence(target.CatalogRead().Epoch())
			}
			if ctx.Err() != context.Canceled || !boundary.observed || target.domain != owner {
				t.Fatal("boundary not exercised", boundary.observed, err)
			}
			if late {
				if err != nil || target.CatalogRead().Epoch() != before.Epoch()+1 || target.commitSeq != frontier+1 {
					t.Fatal("late cancellation retracted install", err)
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
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
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
