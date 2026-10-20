package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
)

type StatementDescription struct {
	Parameters ParameterInfo
	Results    []catalog.Column
	// Logical user identities belong to an owner/epoch, not merely a reused OID.
	catalogOwner *catalog.Owner
	catalogEpoch ids.CatalogEpoch
	// Public signatures stay immutable. Invalidation refreshes temporal-now and
	// specialized groups atomically, preserving native logical literal identity.
	temporals        []temporalConstant
	composites       map[preparedCompositeKey]compositeOccurrence
	sequences        *sequence.Analysis
	regtypes         *regtypeAnalysis
	analysis         *preparedAnalysis
	quotes           []exec.QuoteConstant
	quoteGeneration  uint64 // Published separately after successful quote preflight.
	planOwner        ids.SessionToken
	planGeneration   uint64
	refreshTemporals bool // Candidate-only request; never published in a description.
}
