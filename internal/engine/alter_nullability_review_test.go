package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

func TestAlterNullabilityReviewOwnedSequenceReplacement(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, restore := range []bool{false, true} {
			for _, at := range []int{1, 2} {
				t.Run(fmt.Sprintf("explicit=%t/restore=%t/validation=%d", explicit, restore, at), func(t *testing.T) {
					e := New()
					s, startErr := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
					if startErr != nil {
						t.Fatal(startErr)
					}
					completed := false
					t.Cleanup(func() {
						if !completed {
							return
						}
						s.RequestEnd(CleanClose)
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						if err := s.WaitEnd(ctx); err != nil {
							t.Error(err)
						}
					})
					sessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
					sessionSQL(t, s, "INSERT INTO t VALUES(7,9)")
					sessionSQL(t, s, "CREATE SEQUENCE owned OWNED BY t.b")
					ref := roleTableRef(t, e, "t")
					oldDescriptor, oldStorage := e.catalog.state.Get(ref.Address), e.storage[ref.Address]
					sequence := roleTableRef(t, e, "owned")
					oldSequence, oldGenerator := e.catalog.state.Get(sequence.Address), e.sequences[sequence.Address]
					var tx ids.TxRef
					if explicit {
						tx = transactionBegin(t, s)
					}
					original := e.catalog.Providers[ids.RelationClassID]
					calls := 0
					entered := make(chan struct{})
					e.catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
						calls++
						if calls != at {
							return
						}
						e.catalog.Providers[ids.RelationClassID] = original
						close(entered)
						var err error
						if restore {
							err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(a INT);INSERT INTO sentinel VALUES(91)"))
						} else {
							err = e.Reset(context.Background())
						}
						if err != nil {
							panic(err)
						}
					}}
					done := make(chan error, 1)
					go func() {
						op, err := s.Acquire(context.Background())
						if err != nil {
							done <- err
							return
						}
						_, err = e.Execute(op, statement(t, "ALTER TABLE t DROP b, ALTER a SET NOT NULL"), 0, nil)
						op.Done()
						if explicit && err == nil {
							op, err = s.Acquire(context.Background())
							if err == nil {
								_, err = e.Commit(op, tx)
								op.Done()
							}
						}
						if explicit && at == 1 {
							op, endErr := s.Acquire(context.Background())
							if endErr == nil {
								_, endErr = e.Rollback(op, tx)
								op.Done()
							}
							if endErr != nil {
								done <- endErr
								return
							}
						}
						done <- err
					}()
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						t.Fatal("owned-sequence ALTER did not enter provider callback")
					}
					select {
					case err := <-done:
						completed = true
						if !errors.Is(err, ids.ErrCatalogReplaced) {
							t.Fatal(err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("owned-sequence ALTER retained replacement read lock over provider callback")
					}
					if calls != at || e.catalog.state.Get(ref.Address) == oldDescriptor || e.storage[ref.Address] == oldStorage {
						t.Fatal("stale owned sequence/list installed", calls)
					}
					lockEmpty(t, e)
					if e.catalog.state.Get(sequence.Address) == oldSequence || e.sequences[sequence.Address] == oldGenerator {
						t.Fatal("stale owned sequence resurrected")
					}
					if !e.replacementMu.TryLock() {
						t.Fatal("replacement reservation leaked after completion")
					}
					e.replacementMu.Unlock()
					if restore {
						result := sessionSQL(t, s, "SELECT a FROM sentinel")
						if got := tuples(result); len(got) != 1 || got[0][0] != int64(91) {
							t.Fatal(got)
						}
					}
				})
			}
		}
	}
}
