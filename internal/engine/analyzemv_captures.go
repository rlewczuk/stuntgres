package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Both installed and SQL routines use the existing occurrence identity map.
// Syntax markers demand a capture, but never grant catalog authority themselves.
func (c *viewCapture) CaptureRoutineReference(path viewLiteralPath, ref ids.ObjectRef) error {
	if c.RoutineRefs == nil {
		c.RoutineRefs = make(map[viewLiteralPath]ids.ObjectRef)
	}
	if old, exists := c.RoutineRefs[path]; exists && old != ref {
		return fmt.Errorf("stuntgres: conflicting frozen routine occurrence")
	}
	c.RoutineRefs[path] = ref
	return nil
}

// Default origin is statement-private authority, not a fabricated pg_attrdef for
// a domain recipe. Views retain this origin while mapping to physical slots.
type defaultOrigin struct {
	Definition catalog.ColumnDefault
	Consumer   *catalog.Relation
	Quote      exec.QuoteOrigin
	Requested  bool // UPDATE explicitly selected DEFAULT, including fresh NULL.
}

type regtypeConstant struct {
	Offset     int
	Operator   sqltype.ID
	TypeID     sqltype.ID
	ID         uint32
	Value      sqltype.Value
	Qualified  []ids.ObjectAddress
	Operands   []catalog.ExpressionDependency
	References []regObjectReference
}

type regObjectReference struct {
	Address       ids.ObjectAddress
	Name          string
	Namespace     ids.ObjectAddress
	NamespaceName string
}

type temporalConstant struct {
	Offset       int
	Scalar       int64
	Typmod       int32
	TypeID       sqltype.ID
	West         int32
	Months, Days int32
	Typed        bool
	TypedTypmod  int32
	Value        sqltype.Value // immutable analyzed compound or enum literal
}

type viewColumn struct {
	Frame    uint32
	Slot     int
	RuleRef  ids.ObjectRef
	RuleSide uint32
}

// Namespace labels and frame slots are immutable analysis metadata, not an
// execution node. Leaf/attribute identities survive current positional changes;
// computed USING slots have no leaf identity.
type viewNamespaceColumn struct {
	Name       string
	Slot, Leaf int
	Attribute  int32
}

type viewNamespaceQualifier struct {
	Name    string
	Columns []viewNamespaceColumn
}

type viewNamespace struct {
	Frame      uint32
	Node       uint8
	Ordinal    int
	Columns    []viewNamespaceColumn
	Qualifiers []viewNamespaceQualifier
	Keys       []string
}

type compositeOccurrenceKind uint8

const (
	CompositeSelection compositeOccurrenceKind = iota + 1
	CompositeExpansion
	CompositeDotBuiltin
	CompositeWholeRow
	CompositeSourceExpansion
	CompositeRecordArgument
)

type compositeOccurrence struct {
	Kind       compositeOccurrenceKind
	Base       sqltype.Usage
	Attributes []int32
	Names      []string // Original exposed labels for a selected source star.
	Function   functions.NullCountKind
	Source     plan.WholeRowSource
}

type preparedCompositeKey struct {
	Offset     int
	Kind       compositeOccurrenceKind
	Projection int // Star offset; one source can occur in several target stars.
}

func (p *preparedCaptures) CaptureComposite(key preparedCompositeKey, base sqltype.Usage, attributes []int32) {
	if p.Composites == nil {
		p.Composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	p.Composites[key] = compositeOccurrence{Kind: key.Kind, Base: base, Attributes: attributes}
}

func (c *viewCapture) CaptureViewComposite(path viewLiteralPath, kind compositeOccurrenceKind, base expr.Expr, attributes []int32) {
	if c.Composites == nil {
		c.Composites = make(map[viewLiteralPath]compositeOccurrence)
	}
	source := plan.WholeRowSource{}
	if row, ok := base.(plan.RowExpr); ok {
		source = row.WholeSource
	}
	c.Composites[path] = compositeOccurrence{Kind: kind, Base: base.ResultUsage(), Attributes: attributes, Source: source}
}

type viewLiteralClause uint8

const (
	ViewTargetLiteral viewLiteralClause = iota
	ViewWhereLiteral
	ViewGroupLiteral
	ViewHavingLiteral
	ViewJoinLiteral
	ViewOrderLiteral
	ViewOffsetLiteral
	ViewLimitLiteral
	ViewValuesCellLiteral
	ViewValuesOrderLiteral
	ViewValuesOffsetLiteral
	ViewValuesLimitLiteral
	ViewWindowLiteral
	ViewDistinctOnLiteral
	ViewFunctionLiteral
)

// Each lowered syntax occurrence has its own structural key. BETWEEN can bind
// the same source position independently to different types in its two branches.
type viewLiteralPath struct {
	Query       uint32
	Clause      viewLiteralClause
	Target      int
	Node        int
	Source, Row int
	// Zero names the raw occurrence; positive values name retained implicit
	// coercions of a selected parent's arguments without consuming raw ordinals.
	Argument int
}
