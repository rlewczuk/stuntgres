package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
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
	sequences        *sequenceAnalysis
	regtypes         *regtypeAnalysis
	analysis         *preparedAnalysis
	quotes           []quoteConstant
	quoteGeneration  uint64 // Published separately after successful quote preflight.
	planOwner        ids.SessionToken
	planGeneration   uint64
	refreshTemporals bool // Candidate-only request; never published in a description.
}

type temporalConstant struct {
	offset       int
	scalar       int64
	typmod       int32
	typeID       sqltype.ID
	west         int32
	months, days int32
	typed        bool
	typedTypmod  int32
	value        sqltype.Value // immutable analyzed compound or enum literal
}

func refreshableTemporalCapture(c temporalConstant) bool {
	t := c.value.Type()
	if statementCatalogType(t) {
		return false // Logical compounds retain even their embedded temporal fields.
	}
	id := c.typeID
	if t != nil {
		for t.IsArray() {
			t = t.Element()
		}
		id = t.ID()
	}
	switch id {
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.Regrole:
		return true
	}
	return false
}
