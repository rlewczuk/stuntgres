package core

import (
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// PrepareClientEncodingForTest requires Engine.Mu, just like the production
// installation frontier. It exposes no production inspection or mutation API.
func PrepareClientEncodingForTest(e *Engine, op *session.Operation, id int) (sqltype.ClientEncodingPolicy, error) {
	return e.prepareClientEncodingLocked(op, id)
}

func PrepareCopyEncodingForTest(s *StatementScope, options *copyfmt.Options, from bool) error {
	return s.prepareCopyEncoding(options, from)
}
