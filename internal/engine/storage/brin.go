package storage

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// BrinBinding captures a typed scalar summary key, never a B-tree ordering ABI.
type BrinBinding struct {
	Slot       int
	Expression expr.Expr
	Derived    *catalog.DerivedIndexKey
	Usage      sqltype.Usage
	Class      sqltype.IndexClass
	Comparison sqltype.BrinComparison
}

// BrinSummary is conservative: ending a row need not remove an extremum or flag.
// Values are immutable owned datums; no row-key vector is retained.
type BrinSummary struct {
	HasNull, HasNonNull bool
	Min, Max            sqltype.Value
}

type BrinRange struct {
	Summarized bool
	Keys       []BrinSummary
}

// One authoritative version-vector slot is one logical page. ALTER reloptions
// retain intent on the descriptor; only a rebuild changes this effective width.
type BrinRoot struct {
	EffectivePagesPerRange int
	Bindings               []BrinBinding
	Ranges                 []BrinRange
	Storage                *Relation
	Generation             uint64
	Prefix                 int
	Ends                   *DeletionRoot
}
