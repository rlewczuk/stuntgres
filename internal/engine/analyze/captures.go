package analyze

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
func (c *ViewCapture) CaptureRoutineReference(path ViewLiteralPath, ref ids.ObjectRef) error {
	if c.RoutineRefs == nil {
		c.RoutineRefs = make(map[ViewLiteralPath]ids.ObjectRef)
	}
	if old, exists := c.RoutineRefs[path]; exists && old != ref {
		return fmt.Errorf("stuntgres: conflicting frozen routine occurrence")
	}
	c.RoutineRefs[path] = ref
	return nil
}

// Default origin is statement-private authority, not a fabricated pg_attrdef for
// a domain recipe. Views retain this origin while mapping to physical slots.
type DefaultOrigin struct {
	Definition catalog.ColumnDefault
	Consumer   *catalog.Relation
	Fold       exec.FoldOrigin
	Requested  bool // UPDATE explicitly selected DEFAULT, including fresh NULL.
}

type RegtypeConstant struct {
	Offset     int
	Operator   sqltype.ID
	TypeID     sqltype.ID
	ID         uint32
	Value      sqltype.Value
	Qualified  []ids.ObjectAddress
	Operands   []catalog.ExpressionDependency
	References []RegObjectReference
}

type RegObjectReference struct {
	Address       ids.ObjectAddress
	Name          string
	Namespace     ids.ObjectAddress
	NamespaceName string
}

type TemporalConstant struct {
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

type ViewColumn struct {
	Frame      uint32
	Slot       int
	RuleRef    ids.ObjectRef
	RuleSide   uint32
	TableOID   bool
	Owner      ids.ObjectRef
	Scope      uint32
	Occurrence int
}

// Namespace labels and frame slots are immutable analysis metadata, not an
// execution node. Leaf/attribute identities survive current positional changes;
// computed USING slots have no leaf identity.
type ViewNamespaceColumn struct {
	Name       string
	Slot, Leaf int
	Attribute  int32
}

type ViewNamespaceQualifier struct {
	Name    string
	Columns []ViewNamespaceColumn
}

type ViewNamespace struct {
	Frame      uint32
	Node       uint8
	Ordinal    int
	Columns    []ViewNamespaceColumn
	Qualifiers []ViewNamespaceQualifier
	Keys       []string
}

type TypeOccurrenceKind uint8

const (
	CompositeSelection TypeOccurrenceKind = iota + 1
	CompositeExpansion
	CompositeDotBuiltin
	CompositeWholeRow
	CompositeSourceExpansion
	CompositeRecordArgument
	DomainCast
	TypeNameCast
	IdentityCast      // Non-domain user type; shares occurrence lifetime, not domain stamps.
	RecordConstructor // Exact immutable anonymous shape; current fields still bind first.
)

type TypeOccurrence struct {
	Kind       TypeOccurrenceKind
	Base       sqltype.Usage
	Attributes []int32
	Names      []string // Original exposed labels for a selected source star.
	Function   functions.NullCountKind
	Source     plan.WholeRowSource
}

type PreparedTypeKey struct {
	Offset     int
	Kind       TypeOccurrenceKind
	Projection int // Star offset; one source can occur in several target stars.
}

func (p *PreparedCaptures) CaptureComposite(key PreparedTypeKey, base sqltype.Usage, attributes []int32) {
	if p.Types == nil {
		p.Types = make(map[PreparedTypeKey]TypeOccurrence)
	}
	p.Types[key] = TypeOccurrence{Kind: key.Kind, Base: base, Attributes: attributes}
}

func (c *ViewCapture) CaptureViewComposite(path ViewLiteralPath, kind TypeOccurrenceKind, base expr.Expr, attributes []int32) {
	if c.Composites == nil {
		c.Composites = make(map[ViewLiteralPath]TypeOccurrence)
	}
	source := plan.WholeRowSource{}
	if row, ok := base.(plan.RowExpr); ok {
		source = row.WholeSource
	}
	c.Composites[path] = TypeOccurrence{Kind: kind, Base: base.ResultUsage(), Attributes: attributes, Source: source}
}

type ViewLiteralClause uint8

const (
	ViewTargetLiteral ViewLiteralClause = iota
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
type ViewLiteralPath struct {
	Query       uint32
	Clause      ViewLiteralClause
	Target      int
	Node        int
	Source, Row int
	// Zero names the raw occurrence; positive values name retained implicit
	// coercions of a selected parent's arguments without consuming raw ordinals.
	Argument int
}
