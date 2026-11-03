package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// These assertions supplement the public DROP scenarios with the canonical
// assignment frontier, completion ownership and actual private row journal.
func originSessionDropAssignmentWindows(t *testing.T) {
	t.Helper()
	for _, tracking := range []bool{false, true} {
		for _, assigned := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("tracking_%t_drop_assigned_%t_held_%t", tracking, assigned, held), func(t *testing.T) {
					e := engine.NewWithSettings(nil, "", tracking)
					s := enginetest.StartSession(t, e, nil)
					originSessionConfigurePublication(t, e, s)
					enginetest.SessionSQL(t, s, "CREATE TABLE public.origin_drop_private(v integer)")
					enginetest.SessionSQL(t, s, "INSERT INTO public.origin_drop_private VALUES(10)")
					enginetest.SessionSQL(t, s, "SELECT pg_replication_origin_xact_setup('0/90','2022-03-04 05:06:07+00')")
					e.Mu.Lock()
					frontier := e.Visibility.LastAssignedXID()
					lastXID, history := e.Visibility.LastCommittedXID(), e.Visibility.LastCommitTimestamp()
					beforeRef := s.Transactions.Ref
					beforeEntry := e.Visibility.Entries[beforeRef]
					position, present := e.CopyReplicationOriginProgressLocked()[42]
					e.Mu.Unlock()
					if !present || position != 0x10 {
						t.Fatal("fixture did not establish accumulated progress", position, present)
					}
					ast, description := enginetest.PreparedDescription(t, e, s, "SELECT pg_replication_origin_drop('origin_publication_unit')")
					e.Mu.Lock()
					prepareChanged := e.Visibility.LastAssignedXID() != frontier || e.Visibility.LastCommittedXID() != lastXID || e.Visibility.LastCommitTimestamp() != history || s.Transactions.Ref != beforeRef || e.Visibility.Entries[beforeRef] != beforeEntry
					e.Mu.Unlock()
					if prepareChanged {
						t.Fatal("Prepare assigned a canonical identity or published history")
					}
					var ref ids.TxRef
					var abortedXID uint32
					if assigned {
						ref = enginetest.TransactionBegin(t, s)
						enginetest.SessionSQL(t, s, "INSERT INTO public.origin_drop_private VALUES(90)")
						e.Mu.Lock()
						entry := e.Visibility.Entries[ref]
						abortedXID = entry.AssignedXID
						missingAssignment := abortedXID == 0 || entry.FullXID <= frontier || len(s.Transactions.Writes) == 0
						frontier = e.Visibility.LastAssignedXID()
						e.Mu.Unlock()
						if missingAssignment {
							t.Fatal("real write did not own its assigned private journal", entry)
						}
					}
					var result core.Result
					var err error
					if held {
						result, err = enginetest.PreparedExecute(e, s, ast, &description)
					} else {
						op, acquireErr := s.Acquire(context.Background())
						if acquireErr != nil {
							t.Fatal(acquireErr)
						}
						result, err = e.Execute(op, ast, 0, nil)
						op.Done()
					}
					if result.Rows != nil {
						result.Rows.Close()
						t.Fatal("rejected DROP published a result cursor")
					}
					enginetest.WaitDiagnostic(t, err, "55006", fmt.Sprintf("could not drop replication origin with ID 42, in use by PID %d", s.Token().ID()))
					e.Mu.Lock()
					position, present = e.CopyReplicationOriginProgressLocked()[42]
					want := uint64(0x10)
					if assigned {
						want = 0x90
					}
					badCompletion := !present || position != want || e.Visibility.LastAssignedXID() != frontier || e.Visibility.LastCommittedXID() != lastXID || e.Visibility.LastCommitTimestamp() != history
					if !assigned && (s.Transactions.Ref != beforeRef || e.Visibility.Entries[beforeRef] != beforeEntry) {
						badCompletion = true
					}
					retainedAuthority := assigned && (e.Visibility.Entries[ref].AssignedXID != 0 || e.Visibility.LookupCommitTimestampLocked(abortedXID).Present || len(s.Transactions.Writes) != 0)
					e.Mu.Unlock()
					if badCompletion {
						t.Fatal("DROP confused assignment, abort progress or committed history", position, want)
					}
					if retainedAuthority {
						t.Fatal("actual failed transaction retained completion/write authority")
					}
					if assigned {
						s.Transactions.Mu.Lock()
						state := s.Transactions.State
						s.Transactions.Mu.Unlock()
						if state != session.TxAborted {
							t.Fatal("DROP did not traverse failed-explicit-transaction disposal", state)
						}
						enginetest.TransactionEnd(t, s, ref, false)
					} else {
						enginetest.SessionSQL(t, s, "INSERT INTO public.origin_drop_private VALUES(90)")
						e.Mu.Lock()
						missingWrite := e.CopyReplicationOriginProgressLocked()[42] != 0x90 || e.Visibility.LastAssignedXID() <= frontier || e.Visibility.LastCommitTimestamp() != history
						e.Mu.Unlock()
						if missingWrite {
							t.Fatal("rejected DROP erased the pending context or next-write assignment")
						}
					}
					e.Mu.Lock()
					address := e.Catalog.Current().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "origin_drop_private"})
					rows := 0
					for _, version := range e.Storage.Relations[address].Versions {
						if version.Born != 0 {
							rows++
						}
					}
					e.Mu.Unlock()
					wantRows := 2
					if assigned {
						wantRows = 1
					}
					if rows != wantRows {
						t.Fatal("aborted row reached canonical storage", rows, wantRows)
					}
				})
			}
		}
	}
}
