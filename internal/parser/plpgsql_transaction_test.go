package parser

import "testing"

func TestPLpgSQLTransactionInstruction(t *testing.T) {
	for _, tc := range []struct {
		text  string
		kind  TransactionControlKind
		chain bool
	}{
		{"COMMIT", KindCommit, false}, {"COMMIT AND CHAIN", KindCommit, true}, {"COMMIT AND NO CHAIN", KindCommit, false},
		{"ROLLBACK", KindRollback, false}, {"ROLLBACK AND CHAIN", KindRollback, true}, {"ROLLBACK AND NO CHAIN", KindRollback, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			block, err := ParsePLpgSQL("BEGIN "+tc.text+";END", PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			in := block.Statements[0]
			if in.Block != nil {
				in = in.Block.Statements[0]
			}
			if in.Transaction == nil || in.Transaction.Kind != tc.kind || in.Transaction.Chain != tc.chain || in.Pos.Offset != 6 || in.Pos.Character != 7 || !ValidPLTransaction(in.Transaction, in.Pos) {
				t.Fatal(in)
			}
			copy := *in.Transaction
			copy.Kind = KindStart
			if ValidPLTransaction(&copy, in.Pos) {
				t.Fatal("forged kind accepted")
			}
			copy = *in.Transaction
			copy.Chain = !copy.Chain
			if ValidPLTransaction(&copy, in.Pos) {
				t.Fatal("forged options accepted")
			}
			again, err := ParsePLpgSQL("BEGIN ROLLBACK;END", PLReturnPolicy{})
			if err != nil || again == block || in.Transaction.Kind != tc.kind {
				t.Fatal("parse backing not independent", err)
			}
		})
	}
	for _, text := range []string{"COMMIT WORK", "COMMIT TRANSACTION", "ROLLBACK WORK", "COMMIT AND", "ROLLBACK AND NO", "COMMIT AND CHAIN CHAIN", "COMMIT;garbage", "ABORT", "END TRANSACTION"} {
		if _, err := ParsePLpgSQL("BEGIN "+text+";END", PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed procedural control", text)
		}
	}
}
