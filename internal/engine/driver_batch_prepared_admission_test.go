package engine

import (
	"context"
	"testing"
)

func TestDriverBatchPreparedControlSegmentAdmission(t *testing.T) {
	for _, end := range []string{"", "COMMIT", "ROLLBACK"} {
		for _, control := range []string{"PREPARE TRANSACTION 'admission'", "COMMIT PREPARED 'admission'", "ROLLBACK PREPARED 'admission'"} {
			t.Run(end+"_"+control, func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				m := batchUnitStart(t, s, context.Background())
				defer m.end(context.Canceled)
				var preceding TxRef
				if end != "" {
					if _, err := m.step(statement(t, "BEGIN")); err != nil {
						t.Fatal(err)
					}
					preceding = s.transactions.ref
					if _, err := m.step(statement(t, end)); err != nil {
						t.Fatal(err)
					}
					next, err := m.scope.NextStatement()
					if err != nil {
						t.Fatal(err)
					}
					m.scope = next
				}
				if s.transactions.state != TxIdle {
					t.Fatal("fixture did not reach idle segment boundary")
				}
				// Test the actual canonical gate before execution/registry lookup.
				if err := m.scope.gate(statement(t, control)); err != nil {
					t.Fatal(err)
				}
				tx := s.transactions
				if tx.state != TxImplicit || !tx.inBlock() || tx.ref == (TxRef{}) || tx.ref == preceding || tx.ref != m.scope.txRef || tx.ticket == nil || tx.ticket != m.scope.ticket || tx.ref.epoch != e.state.epoch || !m.scope.implicit {
					t.Fatal("reached prepared control bypassed ordinary segment authority")
				}
				if len(e.prepared) != 0 || e.commitSeq != 0 {
					t.Fatal("admission touched prepared registry or committed data")
				}
			})
		}
	}
}
