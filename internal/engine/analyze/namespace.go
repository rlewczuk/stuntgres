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
	TableOIDOccurrence            int  // Eligible real source, one-based; zero means no implicit attribute.
	SystemColumnsHidden           bool // JOIN operands remain qualified-only.
}

// QueryNamespace is what a row consumer makes visible: its columns,
// qualifiers and frame schema. Stored row expressions build one explicitly;
// nil and empty namespaces expose no local input rows.
type QueryNamespace struct {
	Columns                   []QueryColumn
	Qualifiers                []QueryQualifier
	Frame                     []catalog.Column
	Values                    bool // Synthetic VALUES names retain PostgreSQL spelling hints.
	IdentityValuesOrderDemand []bool
}

// RelationNamespace is the namespace of one relation for an expression bound
// over it (a CHECK, generation, key or USING expression): its columns in
// frame order, read from leaf, under one qualifier named after the relation
// and its schema in view.
func RelationNamespace(view catalog.Read, relation *catalog.Relation, ref ids.ObjectRef, leaf int) QueryNamespace {
	columns := make([]QueryColumn, len(relation.Columns))
	for i, column := range relation.Columns {
		columns[i] = QueryColumn{Descriptor: column, Slot: i, Leaf: leaf, Physical: i}
	}
	qualifier := QueryQualifier{Name: relation.Name(), Original: relation.Name(), Schema: view.Get(relation.Namespace()).Name(), Ref: ref, Columns: columns}
	return QueryNamespace{Columns: columns, Qualifiers: []QueryQualifier{qualifier}, Frame: relation.Columns}
}
