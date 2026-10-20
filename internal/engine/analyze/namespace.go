package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// A target column shares the UPDATE frame, but never source SELECT authority.
const QueryTargetLeaf = -2

// QueryColumn is one visible column of a query level: its descriptor, its
// slot in the level's frame, and the source leaf and physical attribute it
// reads (or -1 for a computed merged value).
type QueryColumn struct {
	Descriptor     catalog.Column
	Slot           int
	Leaf, Physical int // -1 identifies a computed merged value.
	CTEScan        *plan.DerivedNode
	HideStar       bool // Generated CYCLE columns are explicit-only in their owning WITH definitions.
}

// QueryQualifier is a range-table name a query level can qualify columns
// with: a table, alias, CTE, derived table or function source.
type QueryQualifier struct {
	Name, Schema, Original, Alias string
	Ref                           ids.ObjectRef
	Columns                       []QueryColumn
	ScalarAlias                   bool // The admitted producer declares an alias value as its scalar result.
	ExcludeColumnDiagnostics      bool // Whole-join and USING aliases are not error RTE candidates.
	SourceRoot                    plan.Node
	NullExtended                  bool
	LateralForbidden              bool // A DML target cannot correlate into its source, even with LATERAL.
}

// QueryNamespace is what a FROM item makes visible: its columns, its
// qualifiers and its frame schema. Consumers outside queries (checks,
// defaults, index and policy expressions) build one for their relation.
type QueryNamespace struct {
	Columns                   []QueryColumn
	Qualifiers                []QueryQualifier
	Frame                     []catalog.Column
	Values                    bool // Synthetic VALUES names retain PostgreSQL spelling hints.
	IdentityValuesOrderDemand []bool
}
