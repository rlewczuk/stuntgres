package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestDriverBatchPreparedControlSegmentAdmission(t *testing.T) {
	for _, end := range []string{"", "COMMIT", "ROLLBACK"} {
		for _, control := range []string{"PREPARE TRANSACTION 'admission'", "COMMIT PREPARED 'admission'", "ROLLBACK PREPARED 'admission'"} {
			t.Run(end+"_"+control, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				m := batchUnitStart(t, s, context.Background())
				defer m.end(context.Canceled)
				var preceding ids.TxRef
				if end != "" {
					if _, err := m.step(statement(t, "BEGIN")); err != nil {
						t.Fatal(err)
					}
					preceding = s.Transactions.Ref
					if _, err := m.step(statement(t, end)); err != nil {
						t.Fatal(err)
					}
					next, err := m.scope.NextStatement()
					if err != nil {
						t.Fatal(err)
					}
					m.scope = next
				}
				if s.Transactions.State != session.TxIdle {
					t.Fatal("fixture did not reach idle segment boundary")
				}
				// Test the actual canonical gate before execution/registry lookup.
				if err := m.scope.gate(statement(t, control)); err != nil {
					t.Fatal(err)
				}
				tx := s.Transactions
				if tx.State != session.TxImplicit || !tx.InBlock() || tx.Ref == (ids.TxRef{}) || tx.Ref == preceding || tx.Ref != m.scope.txRef || tx.Ticket == nil || tx.Ticket != m.scope.ticket || tx.Ref.Epoch() != e.catalog.State.Epoch() || !m.scope.implicit {
					t.Fatal("reached prepared control bypassed ordinary segment authority")
				}
				if e.prepared.Len() != 0 || e.visibility.CommitSeq() != 0 {
					t.Fatal("admission touched prepared registry or committed data")
				}
			})
		}
	}
}
