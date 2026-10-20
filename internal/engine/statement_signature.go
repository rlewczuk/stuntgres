package engine

import "stuntgres/internal/engine/analyze"

func signatureOf(d StatementDescription) analyze.Signature {
	return analyze.Signature{Parameters: d.Parameters, Results: d.Results, Owner: d.catalogOwner, Epoch: d.catalogEpoch}
}

func checkStatementSignature(expected, actual StatementDescription) error {
	return analyze.CheckSignature(signatureOf(expected), signatureOf(actual))
}
