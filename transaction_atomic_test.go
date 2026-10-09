package stuntgres_test

import (
	"database/sql"
)

// Compile-time consumer shape witnesses keep direct/prepared Conn/Tx helpers
// on the same public APIs used by native and real-reference behavior tests.
var _ transactionExecutor = (*sql.Conn)(nil)
var _ transactionExecutor = (*sql.Tx)(nil)
