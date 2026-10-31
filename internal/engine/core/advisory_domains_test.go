package core_test

import (
	"stuntgres/internal/engine/ids"
)

func withTxID(ref ids.TxRef, id ids.TxID) ids.TxRef {
	return ref.Domain().Tx(ref.Session(), id, ref.Epoch(), ref.Attachment(), ref.Diagnostic())
}
