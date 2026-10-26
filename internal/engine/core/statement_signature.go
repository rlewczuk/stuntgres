package core

import "stuntgres/internal/engine/analyze"

func signatureOf(d Description) analyze.Signature {
	return analyze.Signature{Parameters: d.Parameters, Results: d.Results, Owner: d.CatalogOwner, Epoch: d.CatalogEpoch}
}

func CheckStatementSignature(expected, actual Description) error {
	return analyze.CheckSignature(signatureOf(expected), signatureOf(actual))
}
