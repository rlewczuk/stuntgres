package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestControlTransactionsEndMatrix(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		for _, writer := range []bool{false, true} {
			for _, state := range []string{"active", "stale_failure", "sql_failure"} {
				for _, end := range []string{"COMMIT", "END AND NO CHAIN", "COMMIT AND CHAIN", "ROLLBACK", "ABORT AND CHAIN"} {
					t.Run(replacement+"/"+map[bool]string{false: "reader", true: "writer"}[writer]+"/"+state+"/"+end, func(t *testing.T) {
						e := New()
						a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
						token := a.SessionToken
						sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
						sessionSQL(t, b, "INSERT INTO t VALUES(77)")
						controlsRequire(t, a, "BEGIN")
						if writer {
							sessionSQL(t, a, "INSERT INTO t VALUES(1)")
						}
						old := a.Transactions.Ref
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
						frontier := e.visibility.CommitSeq()
						if state == "stale_failure" {
							for range 2 {
								op := transactionOperation(t, a)
								_, err = e.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
								op.Done()
								if !errors.Is(err, ids.ErrCatalogReplaced) {
									t.Fatal(err)
								}
							}
						}
						err = controlsExecute(t, a, end)
						commit := strings.HasPrefix(end, "COMMIT") || strings.HasPrefix(end, "END")
						if commit {
							if !errors.Is(err, ids.ErrCatalogReplaced) {
								t.Fatal("stale commit", err)
							}
						} else if err != nil {
							t.Fatal(err)
						}
						chain := !commit && strings.HasSuffix(end, "AND CHAIN")
						if a.SessionToken != token || a.Transactions.Writes != nil || e.visibility.CommitSeq() != frontier {
							t.Fatal("replacement/end mutated identity/private writes/frontier")
						}
						if chain {
							if a.TransactionState() != session.TxActive || a.Transactions.Ref.Epoch() != e.CatalogRead().Epoch() || a.Transactions.Ref.ID() <= old.ID() {
								t.Fatal("rollback chain not current")
							}
							controlsRequire(t, a, "ROLLBACK")
						} else if a.TransactionState() != session.TxIdle || a.Transactions.Ref != (ids.TxRef{}) || a.Transactions.Ticket != nil {
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
			owner, stored, frontier := target.catalog.Domain, target.storage.Relations[ref.Address], target.visibility.CommitSeq()
			beforeOID := target.catalog.NextOID()
			var reservedNext ids.OID
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
			preparedStorage := make(map[ids.ObjectAddress]*storage.Relation)
			source.mu.Lock()
			for address, relation := range source.storage.Relations {
				preparedStorage[address] = &storage.Relation{Versions: append([]storage.RowVersion(nil), relation.Versions...)}
			}
			preparedSequences, sequenceErr := sequence.CopyGenerators(replacement, source.storage.Sequences)
			indexes, indexErr := source.captureIndexSnapshot(context.Background(), replacement, source.storage.Relations, source.storage.IndexRoots, source.visibility.CommitSeq())
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
				oids := target.catalog.OIDReservationLocked()
				if _, err := target.catalog.AllocateOIDFromLocked(&oids.Next, 0); err != nil {
					return err
				}
				reservedNext = oids.Next
				next, _, err := target.remapPreparedCatalog(catalog.CloneState(replacement.State()), sourceRoles, targetRoles, nil)
				if err != nil {
					return err
				}
				nextRevision := target.catalog.NextRevision()
				for _, descriptor := range next.Descriptors() {
					if err := access.ValidateACL(target.roles.PublishedRead(&target.catalog.Owner, target.catalog.Read(next)), descriptor); err != nil {
						return err
					}
					nextRevision, _ = catalog.AdvanceRevisionFrontier(nextRevision, descriptor)
				}
				prepared, err := target.prepareForeignIndexStorage(ctx, next, preparedStorage, indexes)
				if err != nil {
					return err
				}
				return target.installForeignCatalogLocked(boundary, next, preparedStorage, preparedSequences, nextRevision, prepared, oids)
			}()
			if err == nil {
				target.locks.Fence(target.catalog.Domain, target.CatalogRead().Epoch())
			}
			if ctx.Err() != context.Canceled || !boundary.observed || target.catalog.Domain != owner {
				t.Fatal("boundary not exercised", boundary.observed, err)
			}
			expectedOID := beforeOID
			if late {
				expectedOID = reservedNext
			}
			if target.catalog.NextOID() != expectedOID {
				t.Fatal("canceled installation changed OID issuance boundary", target.catalog.NextOID(), expectedOID)
			}
			if late {
				if err != nil || target.CatalogRead().Epoch() != before.Epoch()+1 || target.visibility.CommitSeq() != frontier+1 {
					t.Fatal("late cancellation retracted install", err)
				}
				transactionRows(t, b, "SELECT v FROM t", 99)
				if !errors.Is(target.ValidateObjectRef(target.CatalogRead(), ref), ids.ErrCatalogReplaced) {
					t.Fatal("old ref eligible")
				}
				prepared, err := target.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = target.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("old stage publishable", err)
				}
				op := transactionOperation(t, a)
				_, err = target.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
				op.Done()
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if err = controlsExecute(t, a, "COMMIT"); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if target.CatalogRead().State() != before.State() || target.storage.Relations[ref.Address] != stored || target.visibility.CommitSeq() != frontier {
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
			if retained.Count != 1 || retained.Source.View.Data().Catalog.State() != before.State() || testExport(stored.Versions[0].Values[0]) != int64(77) {
				t.Fatal("retained generation changed")
			}
		})
	}
}
