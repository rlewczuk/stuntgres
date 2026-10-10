package parser

import "strings"

// Statement is the closed set of supported statements.
type Statement interface{ statement() }

// SQL names belong to the physical session, not to driver statement handles.
type PrepareSQL struct {
	Name Identifier
	Types []TypeName
	Body Statement
	Source string
	Pos Position
}
func (*PrepareSQL) statement() {}

type ExecuteSQL struct {
	Name Identifier
	Arguments []Expr
	Pos Position
}
func (*ExecuteSQL) statement() {}

type DeallocateSQL struct {
	Name Identifier
	All bool
	Pos Position
}
func (*DeallocateSQL) statement() {}

// TypeName is syntax, not a resolved catalog type.
type TypeName struct {
	Name        string
	Pos         Position
	Quoted      bool
	Schema      *Identifier
	Modifiers   []TypeModifier
	ArrayBounds []int32
	// Standalone regtype syntax retains qualification and SQL-alias provenance.
	Parts  []Identifier
	System bool
	Setof  bool
}

type EnumLabel struct {
	Text string
	Pos  Position
}
type CreateEnum struct {
	Name   ObjectName
	Labels []EnumLabel
	Pos    Position
}

func (*CreateEnum) statement() {}

type CreateComposite struct {
	Name    ObjectName
	Members []Column
	Pos     Position
}

func (*CreateComposite) statement() {}

type CreateDomain struct {
	Name        ObjectName
	Base        TypeName
	Collation   *ObjectName
	Constraints []ColumnConstraint
	Pos         Position
}

func (*CreateDomain) statement() {}

type DropDomain struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropDomain) statement() {}

// TypeModifier retains constant syntax until descriptor-owned resolution.
type TypeModifier struct {
	Integer   *int32
	Raw       string
	UnaryPlus bool
	NonSimple bool // Actual modifier expression; no arithmetic evaluation.
	Pos       Position
}

type Position struct{ Offset, Character int }
type Identifier struct {
	Text string
	Pos  Position
}
type ObjectName struct{ Parts []Identifier }
type Column struct {
	Name        string
	TypeName    TypeName
	Constraints []ColumnConstraint
	Collation   *ObjectName
	Pos         Position
}

type IdentityMode uint8

const (
	IdentityNone IdentityMode = iota
	IdentityAlways
	IdentityByDefault
)

type IdentityDefinition struct {
	Mode             IdentityMode
	Options          []SequenceOption
	Pos              Position
	ConflictingModes bool
}

type GenerationDefinition struct {
	Expression   Expr
	Mode         IdentityMode
	Kind         string
	Pos, ModePos Position
}

type GenerationRestriction struct {
	Code, Message string
	Pos           Position
}
type ForbiddenGenerationExpr struct{ GenerationRestriction }

func (ForbiddenGenerationExpr) expression()                {}
func (e ForbiddenGenerationExpr) SourcePosition() Position { return e.Pos }

type InsertOverride uint8

const (
	OverrideNone InsertOverride = iota
	OverrideSystem
	OverrideUser
)

type ColumnConstraint struct {
	NotNull    bool
	Default    Expr
	Check      *CheckConstraint
	Unique     *UniqueConstraint
	Identity   *IdentityDefinition
	Generation *GenerationDefinition
	Name       Identifier
	Pos        Position
}

type NotNullConstraint struct {
	Column Identifier
	Name   Identifier
	Pos    Position
}

type CheckConstraint struct {
	Name       Identifier
	Expression Expr
	NoInherit  bool
	Pos        Position
}

type UniqueConstraint struct {
	Name    Identifier
	Columns []Identifier
	// Primary distinguishes PRIMARY KEY from UNIQUE on this shared attachment.
	Primary          bool
	NullsNotDistinct bool
	Pos              Position
}

// OnCommitAction is stored with a temporary table's transactional descriptor.
// Unspecified differs from explicit PRESERVE for permanent-table diagnostics.
type OnCommitAction uint8

const (
	OnCommitUnspecified OnCommitAction = iota
	OnCommitPreserve
	OnCommitDelete
	OnCommitDrop
)

type CreateTable struct {
	Name        ObjectName
	Elements    []TableElement
	Pos         Position
	IfNotExists bool
	Persistence string
	OnCommit    OnCommitAction
	OnCommitPos Position
}

func (*CreateTable) statement() {}

type QueryTableForm uint8

const (
	CreateTableAs QueryTableForm = iota
	SelectInto
	CreateMaterializedView
)

type CreateQueryTable struct {
	Name        ObjectName
	Columns     []Identifier
	Query       *Select
	Pos         Position
	Form        QueryTableForm
	IfNotExists bool
	NoData      bool
}

func (*CreateQueryTable) statement() {}

type IndexKey struct {
	Column            Identifier
	Expression        Expr
	Collation         *ObjectName
	Opclass           *ObjectName
	Options           []ViewOption
	Desc              bool
	DirectionExplicit bool
	Nulls             NullPlacement
	Pos               Position
}
type CreateIndex struct {
	Name             Identifier
	Table            ObjectName
	Method           string
	Keys             []IndexKey
	Include          []IndexKey
	Predicate        Expr
	IfNotExists      bool
	Unique           bool
	UniquePos        Position
	NullsNotDistinct bool
	NullsPos         Position
	Options          []IndexOption
	WithPos          Position
	Concurrent       Position
	Pos              Position
}

func (*CreateIndex) statement() {}

// IndexSubqueryExpr consumes forbidden syntax without introducing query execution.
type IndexSubqueryExpr struct{ Pos Position }

func (IndexSubqueryExpr) expression()                {}
func (e IndexSubqueryExpr) SourcePosition() Position { return e.Pos }

type ViewOption struct {
	Name     Identifier
	Value    string
	HasValue bool
}
type CreateView struct {
	Name    ObjectName
	Columns []Identifier
	Options []ViewOption
	Query   *Select
	Pos     Position
	Replace bool
}

func (*CreateView) statement() {}

type AlterView struct {
	Name                    ObjectName
	Action                  string
	Column, Target          Identifier
	Options                 []ViewOption
	OwnerIdentity, IfExists bool
	Pos                     Position
}

func (*AlterView) statement() {}

type DropView struct {
	Names        []ObjectName
	IfExists     bool
	Mode         DropMode
	Pos          Position
	Materialized bool
}

func (*DropView) statement() {}

type SequenceOption struct {
	Name  string
	Value int64
	Type  TypeName
	Owned ObjectName
	Pos   Position
}
type CreateSequence struct {
	Name        ObjectName
	Options     []SequenceOption
	Persistence string
	IfNotExists bool
	Pos         Position
}

func (*CreateSequence) statement() {}

type AlterSequence struct {
	Name          ObjectName
	Options       []SequenceOption
	Action        string
	Target        Identifier
	OwnerIdentity bool
	IfExists      bool
	Pos           Position
}

func (*AlterSequence) statement() {}

type DropSequence struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropSequence) statement() {}

type CallExpr struct {
	Name ObjectName
	Args []Expr
}

func (CallExpr) expression() {}
func (e CallExpr) SourcePosition() Position {
	if len(e.Name.Parts) == 0 {
		return Position{}
	}
	return e.Name.Parts[0].Pos
}

type RegclassExpr struct {
	Child Expr
	Pos   Position
}

func (RegclassExpr) expression()                {}
func (e RegclassExpr) SourcePosition() Position { return e.Pos }

// ArrayExpr retains ordered scalar or array children of ARRAY[...] syntax.
type ArrayExpr struct {
	Elements []Expr
	Pos      Position
}

func (ArrayExpr) expression()                {}
func (e ArrayExpr) SourcePosition() Position { return e.Pos }

type ArraySubscript struct {
	Lower, Upper Expr
	Slice        bool
	Pos          Position
}

type SubscriptExpr struct {
	Base       Expr
	Subscripts []ArraySubscript
	Pos        Position
}

func (SubscriptExpr) expression()                {}
func (e SubscriptExpr) SourcePosition() Position { return e.Pos }

// Children uses caller storage for admitted ranks. Larger raw syntax still
// traverses safely before the binder reports the dimension-limit diagnostic.
func (e SubscriptExpr) Children(storage *[13]Expr) []Expr {
	count := 0
	if e.Base != nil {
		count++
	}
	for _, s := range e.Subscripts {
		if s.Lower != nil {
			count++
		}
		if s.Upper != nil {
			count++
		}
	}
	children := storage[:]
	if count > len(children) {
		children = make([]Expr, count)
	}
	at := 0
	if e.Base != nil {
		children[at] = e.Base
		at++
	}
	for _, s := range e.Subscripts {
		if s.Lower != nil {
			children[at] = s.Lower
			at++
		}
		if s.Upper != nil {
			children[at] = s.Upper
			at++
		}
	}
	return children[:count]
}

type TimeZoneExpr struct {
	Value, Zone Expr
	Local       bool
	Pos         Position
}

func (TimeZoneExpr) expression()                {}
func (e TimeZoneExpr) SourcePosition() Position { return e.Pos }

type CollateExpr struct {
	Child Expr
	Name  ObjectName
	Pos   Position
}

func (CollateExpr) expression()                {}
func (e CollateExpr) SourcePosition() Position { return e.Pos }

type TableElement struct {
	Column  *Column
	Like    *TableLike
	NotNull *NotNullConstraint
	Check   *CheckConstraint
	Unique  *UniqueConstraint
	Pos     Position
}

type TableLike struct {
	Name    ObjectName
	Options []LikeOption
}

type LikeOption struct {
	Name      string
	Including bool
	Pos       Position
}

type DropMode uint8

const (
	DropRestrict DropMode = iota
	DropCascade
)

type DropTable struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropTable) statement() {}

type AlterTableRenameColumn struct {
	Name     ObjectName
	Column   Identifier
	NewName  Identifier
	IfExists bool
	Only     bool
	Pos      Position
}

func (*AlterTableRenameColumn) statement() {}

type AlterTableRenameTable struct {
	Name     ObjectName
	NewName  Identifier
	IfExists bool
	Pos      Position
}

func (*AlterTableRenameTable) statement() {}

type AlterTableSetSchema struct {
	Name     ObjectName
	Schema   Identifier
	IfExists bool
	Only     bool
	Pos      Position
}

func (*AlterTableSetSchema) statement() {}

type AlterTableActionKind uint8

const (
	SetNotNull AlterTableActionKind = iota + 1
	DropNotNull
	AddNotNull
	DropConstraint
	DropColumn
	AddIdentity
	DropIdentity
	SetIdentity
	AlterColumnType
	AddColumn
)

type AlterTableAction struct {
	Column      Identifier
	Constraint  Identifier
	Action      AlterTableActionKind
	IfExists    bool
	IfNotExists bool
	Definition  *Column
	Mode        DropMode
	Pos         Position
	Identity    *IdentityDefinition
	TypeName    TypeName
	Collation   *ObjectName
	Using       Expr
}

type AlterTable struct {
	Name    ObjectName
	Actions []AlterTableAction
	Only    bool
	Pos     Position
}

func (*AlterTable) statement() {}

type DefaultAction uint8

const (
	SetDefault DefaultAction = iota + 1
	DropDefault
)

type AlterTableDefault struct {
	Name       ObjectName
	Column     Identifier
	Only       bool
	Action     DefaultAction
	Expression Expr
	Pos        Position
}

func (*AlterTableDefault) statement() {}

type alterTarget struct {
	Name     ObjectName
	Only     bool
	IfExists bool
}

type dropTarget struct {
	Name     Identifier
	IfExists bool
}

type Insert struct {
	Name          ObjectName
	Targets       []InsertTarget
	Rows          [][]Expr
	Source        *InsertSource
	Pos           Position
	DefaultValues bool
	Override      InsertOverride
	Conflict      *InsertConflict
}

// InsertConflict is absent for ordinary insertion. An empty target means all
// enforcing providers; Keys and Constraint retain distinct inference authority.
type InsertConflict struct {
	Keys       []IndexKey
	Predicate  Expr
	Constraint Identifier
	Pos        Position
}

type InsertTarget struct {
	Name       string
	Pos        Position
	Subscripts []ArraySubscript
}

func (*Insert) statement() {}

// Merge retains only the supported NOT MATCHED BY TARGET insertion class.
// Source and expression subqueries share ordinary query binding/admission.
type Merge struct {
	Name   ObjectName
	Alias  Identifier
	Source FromItem
	On     Expr
	Arms   []MergeInsert
	Pos    Position
}

type MergeInsert struct {
	Condition      Expr
	Targets        []InsertTarget
	Values         []Expr
	DefaultValues  bool
	Override       InsertOverride
	Pos, ActionPos Position
}

func (*Merge) statement() {}

// Undecorated VALUES stays destination-context input, even in parentheses.
// Only decorated VALUES is represented as a query source.
type InsertSource struct {
	Select *Select
	Values *ValuesQuery
}

func insertStatement(name ObjectName, targets []InsertTarget, source *InsertSource, pos Position) *Insert {
	stmt := &Insert{Name: name, Targets: targets, Pos: pos}
	if source.Select == nil && source.Values.Order == nil && source.Values.Bounds == nil {
		stmt.Rows = source.Values.Rows
	} else {
		stmt.Source = source
	}
	return stmt
}

type Delete struct {
	Name      ObjectName
	Alias     Identifier
	Using     FromItem
	Where     Expr
	Returning *Returning
	Pos       Position
}

func (*Delete) statement() {}

type Returning struct {
	Targets []Expr
	Labels  []Identifier
	Aliases []ReturningAlias
	Pos     Position
}

type ReturningAlias struct {
	New   bool
	Alias Identifier
	Pos   Position
}

type Update struct {
	Name        ObjectName
	Alias       Identifier
	From        FromItem
	Assignments []UpdateAssignment
	Where       Expr
	Returning   *Returning
	Pos         Position
}
type UpdateAssignment struct {
	Target      Identifier
	Indirection []Identifier
	Subscripts  []ArraySubscript
	Value       Expr
}

func (*Update) statement() {}

type Select struct {
	From          FromItem
	Values        *ValuesQuery
	Targets       []Expr
	Labels        []Identifier
	Where         Expr
	GroupBy       *GroupClause
	Having        Expr
	Windows       []WindowDefinition
	Order         *Order
	Pos           Position
	Into          *ObjectName
	Locking       []RowLockClause
	Bounds        *QueryBounds
	Distinct      *DistinctClause
	Set           *SetQuery
	Parenthesized bool // Explicit query operand grouping.
}

func (*Select) statement() {}

type GroupKind uint8

const (
	GroupExpression GroupKind = iota
	GroupList
	GroupEmpty
	GroupSets
	GroupRollup
	GroupCube
)

// A nil clause means no grouping; an explicit empty item is never absent.
type GroupClause struct {
	Items         []GroupItem
	Distinct, All bool
	Pos           Position
}

type GroupItem struct {
	Kind  GroupKind
	Expr  Expr
	Items []GroupItem
	Pos   Position
}

func groupExpressionItem(expr Expr) GroupItem {
	if row, ok := expr.(RowExpr); ok && !row.Explicit {
		items := make([]GroupItem, len(row.Fields))
		for i, field := range row.Fields {
			items[i] = groupExpressionItem(field)
		}
		return GroupItem{Kind: GroupList, Items: items, Pos: row.Pos}
	}
	return GroupItem{Kind: GroupExpression, Expr: expr, Pos: expr.SourcePosition()}
}

// VisitExpressions gives capture, rendering and source traversal one stable
// depth-first leaf order, including leaves below nested grouping constructs.
func (g *GroupClause) VisitExpressions(visit func(Expr) error) error {
	if g == nil {
		return nil
	}
	var walk func([]GroupItem) error
	walk = func(items []GroupItem) error {
		for _, item := range items {
			if item.Kind == GroupExpression {
				if err := visit(item.Expr); err != nil {
					return err
				}
			} else if err := walk(item.Items); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(g.Items)
}

type GroupingExpr struct {
	Args []Expr
	Pos  Position
}

func (GroupingExpr) expression()                {}
func (e GroupingExpr) SourcePosition() Position { return e.Pos }

// SQL cursors retain a query privately; each FETCH publishes a detached batch.
type DeclareCursor struct {
	Name  Identifier
	Query *Select
	Pos   Position
}

func (*DeclareCursor) statement() {}

type FetchCursor struct {
	Name     Identifier
	Count    int64
	All      bool
	Backward bool
	Pos      Position
}

func (*FetchCursor) statement() {}

type CloseCursor struct {
	Name Identifier
	All  bool
	Pos  Position
}

func (*CloseCursor) statement() {}

// LeftmostSelect follows set arms only; FROM and expression subqueries are
// separate statement frontiers and must never supply an outer INTO destination.
func LeftmostSelect(query *Select) *Select {
	for query != nil && query.Set != nil {
		query = query.Set.Left
	}
	return query
}

// parenthesizedQuery retains the syntax wrapper's position independently of SELECT.
type parenthesizedQuery struct {
	Query *Select
	Pos   Position
}

// RowLockClause is one positioned trailing locking clause.
type RowLockClause struct {
	Mode    string
	Targets []ObjectName
	Wait    RowLockWait
	Pos     Position
}

type RowLockWait uint8

const (
	RowLockBlock RowLockWait = iota
	RowLockSkip
	RowLockNowait
)

// DISTINCT retains plain execution syntax or the positioned ON expression list.
type DistinctClause struct {
	On  []Expr
	Pos Position
}
type SetQuery struct {
	Left, Right *Select
	Operator    string
	All         bool
	Pos         Position
}

// QueryBounds retains decoration presence even for LIMIT ALL/NULL.
type QueryBounds struct {
	Offset, Count Expr
	WithTies      bool
	WithPosition  Position
}

// WindowSpec preserves the input-key namespace and specification caret owner.
type WindowSpec struct {
	Name      *Identifier
	Partition []Expr
	Order     []OrderKey
	Frame     *RangeFrame
	Pos       Position
	Bare      bool
}

type WindowDefinition struct {
	Name Identifier
	Spec WindowSpec
}

type FrameBoundKind uint8

const (
	FrameUnboundedPreceding FrameBoundKind = iota
	FrameOffsetPreceding
	FrameCurrentRow
	FrameOffsetFollowing
	FrameUnboundedFollowing
)

type FrameBound struct {
	Kind   FrameBoundKind
	Offset Expr
	Pos    Position
}

type RangeFrame struct {
	Start, End FrameBound
	Pos        Position
}

// Parser-only keyword identity is separate from the specification caret and is
// discarded on attachment; it is not part of the persisted expression tree.
type windowOverClause struct {
	spec  WindowSpec
	token lexeme
}

type queryTail struct {
	Bounds  *QueryBounds
	Locking []RowLockClause
}

func singletonArgument(expr Expr) bool {
	switch e := expr.(type) {
	case CaseExpr:
		if e.Test != nil && !singletonArgument(e.Test) {
			return false
		}
		for _, arm := range e.Arms {
			if !singletonArgument(arm.Condition) || !singletonArgument(arm.Result) {
				return false
			}
		}
		return e.Else == nil || singletonArgument(e.Else)
	case ConditionalExpr:
		for _, arg := range e.Args {
			if !singletonArgument(arg) {
				return false
			}
		}
		return true
	case CurrentDateTimeExpr, LiteralExpr, TypedLiteralExpr, ParameterExpr:
		return true
	case CastExpr:
		return singletonArgument(e.Child)
	case CollateExpr:
		return singletonArgument(e.Child)
	case TimeZoneExpr:
		return singletonArgument(e.Value) && (e.Local || singletonArgument(e.Zone))
	case OperatorExpr:
		// Preserve the former signed literal input spelling, not computed
		// arithmetic through the selected singleton projection exception.
		if len(e.Name.Parts) != 1 || (e.Name.Parts[0].Text != "+" && e.Name.Parts[0].Text != "-") || len(e.Args) != 1 {
			return false
		}
		child := e.Args[0]
		for {
			cast, ok := child.(CastExpr)
			if !ok {
				break
			}
			child = cast.Child
		}
		literal, ok := child.(LiteralExpr)
		return ok && (literal.Value.Kind == IntegerValue || literal.Value.Kind == NumericValue)
	case FunctionExpr:
		if len(e.Name.Parts) < 1 || len(e.Name.Parts) > 2 {
			return false
		}
		if !e.ResolutionOnly {
			switch e.Name.Parts[len(e.Name.Parts)-1].Text {
			case "bit_length", "octet_length", "length", "char_length", "character_length", "strpos", "btrim", "ltrim", "rtrim", "overlay", "position", "substring", "substr", "lower", "upper", "casefold", "initcap", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "format":
			case "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "col_description", "obj_description", "shobj_description", "num_nulls", "num_nonnulls", "current_schemas", "random", "random_normal", "setseed", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod", "abs", "ceil", "ceiling", "div", "exp", "factorial", "floor":
			case "pg_collation_actual_version":
			case "pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property":
			case "gen_random_uuid", "uuidv4", "uuidv7":
			default:
				name := e.Name.Parts[len(e.Name.Parts)-1].Text
				if !AdvisorySessionFunctionName(name) && !binaryConversionFunction(name) && !binaryHashFunction(name) && !binaryAccessFunction(name) && !datetimeFunctionName(name) && !stringEncodingFunction(name) && !strings.EqualFold(name, "format") && !strings.HasPrefix(strings.ToLower(name), "pg_get_") {
					return false
				}
			}
		}
		for _, arg := range e.Args {
			if !singletonArgument(arg) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type TableLockMode uint8

const (
	LockAccessShare TableLockMode = iota
	LockRowShare
	LockRowExclusive
	LockShareUpdateExclusive
	LockShare
	LockShareRowExclusive
	LockExclusive
	LockAccessExclusive
)

type LockTarget struct {
	Name ObjectName
	Only bool
}

type LockTable struct {
	Targets []LockTarget
	Mode    TableLockMode
	Pos     Position
	Nowait  bool
}

func (*LockTable) statement() {}

type CommentKind uint8

const (
	CommentTable CommentKind = iota + 1
	CommentColumn
	CommentSchema
	CommentRole
	CommentSequence
)

type Comment struct {
	Kind CommentKind
	Name ObjectName
	Text string
	Null bool
	Pos  Position
}

func (*Comment) statement() {}

type TransactionControlKind uint8

const (
	KindStart TransactionControlKind = iota
	KindCommit
	KindRollback
	KindPrepareTransaction
	KindCommitPrepared
	KindRollbackPrepared
)

type TransactionIsolation uint8

const (
	IsolationDefault TransactionIsolation = iota
	IsolationReadCommitted
	IsolationReadUncommitted
	IsolationRepeatableRead
	IsolationSerializable
)

type TransactionMode struct {
	Isolation     TransactionIsolation
	AccessSet     bool
	ReadOnly      bool
	DeferrableSet bool
	Deferrable    bool
}

type TransactionCharacteristics struct {
	Session bool
	Modes   []TransactionMode
	Pos     Position
}

func (*TransactionCharacteristics) statement() {}

type TransactionControl struct {
	Kind  TransactionControlKind
	Chain bool
	Pos   Position
	Modes []TransactionMode
	GID   string
}

func (*TransactionControl) statement() {}

type SavepointControlKind uint8

const (
	CreateSavepoint SavepointControlKind = iota
	RollbackSavepoint
	ReleaseSavepoint
)

type SavepointControl struct {
	Kind SavepointControlKind
	Name Identifier
	Pos  Position
}

func (*SavepointControl) statement() {}

type Order struct {
	Keys []OrderKey
}

type NullPlacement uint8

const (
	NullsDefault NullPlacement = iota
	NullsFirst
	NullsLast
)

type OrderKey struct {
	Expr  Expr
	Pos   Position
	Desc  bool
	Nulls NullPlacement
}

// OrderConstant classifies SQL92 ORDER/GROUP ordinals without admitting an
// oversized unsigned token merely because its signed result fits int32.
type OrderConstant struct {
	Value Value
	Sign  string
	Real  bool
}

func (c OrderConstant) Ordinal() (int64, bool) {
	if c.Value.Kind != NumericValue || c.Sign == "+" {
		return 0, false
	}
	v := numberValue(lexeme{text: c.Value.Text, numeric: c.Real, pos: c.Value.Pos}, "")
	if v.Kind != IntegerValue || v.Integer > 2147483647 {
		return 0, false
	}
	if c.Sign == "-" {
		return -v.Integer, true
	}
	return v.Integer, true
}

type ValueKind uint8

const (
	NullValue ValueKind = iota
	IntegerValue
	NumericValue
	StringValue
	BooleanValue
	BitStringValue
)

type Value struct {
	Kind    ValueKind
	Integer int64
	Text    string
	Boolean bool
	Pos     Position
}

// Expr is the closed set of admitted scalar syntax occurrences.
type Expr interface {
	expression()
	SourcePosition() Position
}
type LiteralExpr struct{ Value Value }
type TypedLiteralExpr struct {
	TypeName TypeName
	Text     string
	Pos      Position
}
type ColumnExpr struct {
	Name                                    ObjectName
	Pos                                     Position
	QualificationPos, ExtraQualificationPos Position
	// Grouping erases its AST node, but retains the old raw dot boundary.
	QualificationGrouped bool
}
type ParameterExpr struct {
	Number int
	Pos    Position
}

// DefaultExpr requests the destination's default; it is not a scalar value.
type DefaultExpr struct{ Pos Position }

func (DefaultExpr) expression()                {}
func (e DefaultExpr) SourcePosition() Position { return e.Pos }

type OperatorExpr struct {
	Name ObjectName
	Args []Expr
	Pos  Position
	// SQLSimilar distinguishes SQL regex lowering from adjacent POSIX operators.
	SQLSimilar bool
}

type LogicalOperator uint8

const (
	LogicalAnd LogicalOperator = iota
	LogicalOr
	LogicalNot
)

type LogicalExpr struct {
	Op          LogicalOperator
	Left, Right Expr
	Pos         Position
}

type PredicateKind uint8

const (
	IsNull PredicateKind = iota
	IsNotNull
	IsTrue
	IsNotTrue
	IsFalse
	IsNotFalse
	IsUnknown
	IsNotUnknown
	IsDistinct
	IsNotDistinct
)

type PredicateExpr struct {
	Kind        PredicateKind
	Left, Right Expr
	Pos         Position
}

func (LogicalExpr) expression() {}
func (e LogicalExpr) SourcePosition() Position {
	pos := e.Pos
	for _, child := range [...]Expr{e.Left, e.Right} {
		if child != nil {
			p := child.SourcePosition()
			if p.Character > 0 && (pos.Character <= 0 || p.Character < pos.Character) {
				pos = p
			}
		}
	}
	return pos
}
func (PredicateExpr) expression()                {}
func (e PredicateExpr) SourcePosition() Position { return e.Pos }

// IdentityExpr is SQL keyword syntax, not an ordinary catalog function call.
type IdentityExpr struct {
	Name string
	Pos  Position
}

func (IdentityExpr) expression()                {}
func (e IdentityExpr) SourcePosition() Position { return e.Pos }

// CurrentDateTimeExpr retains SQL value-function syntax, never a captured value.
type CurrentDateTimeKind uint8

const (
	CurrentDate CurrentDateTimeKind = iota
	CurrentTime
	CurrentTimestamp
	LocalTime
	LocalTimestamp
)

func (k CurrentDateTimeKind) Name() string {
	switch k {
	case CurrentDate:
		return "current_date"
	case CurrentTime:
		return "current_time"
	case CurrentTimestamp:
		return "current_timestamp"
	case LocalTime:
		return "localtime"
	case LocalTimestamp:
		return "localtimestamp"
	}
	return ""
}

type CurrentDateTimeExpr struct {
	Kind         CurrentDateTimeKind
	Precision    int32
	HasPrecision bool
	Pos          Position
}

func (CurrentDateTimeExpr) expression()                {}
func (e CurrentDateTimeExpr) SourcePosition() Position { return e.Pos }

// FunctionSyntax retains SQL special-form provenance for selected scalar families.
type FunctionSyntax uint8

const (
	OrdinaryFunctionSyntax FunctionSyntax = iota
	SQLFunctionSyntax
	SimilarEscapeFunctionSyntax
)

// StringSyntax retains the selected string family's distinct SQL forms.
// Shared BYTEA TRIM lowering translates TrimStringSyntax to SQLFunctionSyntax.
type StringSyntax uint8

const (
	OrdinaryStringSyntax StringSyntax = iota
	TrimStringSyntax
	NormalizeStringSyntax
	NormalizedStringSyntax
	PositionStringSyntax
	OverlayStringSyntax
	PrefixStringSyntax
)

type FunctionExpr struct {
	Name ObjectName
	Args []Expr
	// ArgNames is nil for positional calls; zero entries mark positional arguments.
	// Args and labels always retain written order, independently of bound slots.
	ArgNames       []Identifier
	Pos            Position
	Variadic       bool
	ResolutionOnly bool // Absent ordinary call: diagnostics, never provider dispatch.
	Syntax         FunctionSyntax
	StringSyntax   StringSyntax
	Window         *WindowSpec
	// Selected time providers retain modifiers only for ordinary-function
	// diagnostics; these fields never authorize aggregate/window execution.
	Star     bool
	Distinct bool
	All      bool
	Order    []OrderKey
	Filter   Expr
}

// AggregateExpr retains ordinary aggregate arguments and call-local modifiers.
type AggregateExpr struct {
	Name      string
	Star      bool
	Args      []Expr
	Order     []OrderKey
	Distinct  bool
	Filter    Expr
	Schema    *Identifier
	Window    *WindowSpec
	FilterPos Position
	Pos       Position
}

func (AggregateExpr) expression()                {}
func (e AggregateExpr) SourcePosition() Position { return e.Pos }

type CastSyntax uint8

const (
	StandardCast CastSyntax = iota
	PostfixCast
	FunctionCast
)

type CastExpr struct {
	Child    Expr
	TypeName TypeName
	Syntax   CastSyntax
	Callee   string
	Pos      Position
}

func (CastExpr) expression() {}
func (e CastExpr) SourcePosition() Position {
	if e.Syntax == PostfixCast {
		return e.Child.SourcePosition()
	}
	return e.Pos
}

func (LiteralExpr) expression()                     {}
func (TypedLiteralExpr) expression()                {}
func (ColumnExpr) expression()                      {}
func (ParameterExpr) expression()                   {}
func (OperatorExpr) expression()                    {}
func (FunctionExpr) expression()                    {}
func (e LiteralExpr) SourcePosition() Position      { return e.Value.Pos }
func (e TypedLiteralExpr) SourcePosition() Position { return e.Pos }
func (e ColumnExpr) SourcePosition() Position       { return e.Pos }
func (e ParameterExpr) SourcePosition() Position    { return e.Pos }
func (e OperatorExpr) SourcePosition() Position     { return e.Pos }
func (e FunctionExpr) SourcePosition() Position     { return e.Pos }

// ExpressionStart differs from an operator's diagnostic token location.
// Grouping is transparent; folded literals already retain their sign location.
func ExpressionStart(expr Expr) Position {
	pos := expr.SourcePosition()
	if subscript, ok := expr.(SubscriptExpr); ok {
		return ExpressionStart(subscript.Base)
	}
	if cast, ok := expr.(CastExpr); ok && cast.Syntax == PostfixCast {
		return ExpressionStart(cast.Child)
	}
	if collate, ok := expr.(CollateExpr); ok {
		return ExpressionStart(collate.Child)
	}
	if operator, ok := expr.(OperatorExpr); ok {
		for _, arg := range operator.Args {
			start := ExpressionStart(arg)
			if start.Offset < pos.Offset {
				pos = start
			}
		}
	}
	return pos
}

// Set uses setting-specific scalar text; Default is distinct from quoted text.
type Set struct {
	Name     Identifier
	Local    bool
	Default  bool
	Values   []string
	Interval *TimeZoneInterval
	Pos      Position
}

func (*Set) statement() {}

type TimeZoneInterval struct {
	Text      string
	Precision int32
	Qualifier string
	Pos       Position
}

type Show struct {
	Name Identifier
	Pos  Position
	All  bool
}

func (*Show) statement() {}

type CreateRole struct {
	Name         Identifier
	DefaultLogin bool
	Options      RoleOptions
}

type InitialRoleMembership struct {
	Kind    string
	Pos     Position
	Members []RoleSpec
	raw     string
}

type RoleSpec struct {
	Name     string
	Identity string
	Pos      Position
}

type MembershipOption struct {
	Name  string
	Value bool
	Pos   Position
}

type RoleMembership struct {
	Targets, Members []RoleSpec
	Options          []MembershipOption
	Grantor          *RoleSpec
	Revoke           bool
	RevokeOption     string
	Cascade          bool
	Alias            bool
}

func (*RoleMembership) statement() {}

type AlterRole struct {
	Name     Identifier
	Identity string
	Options  RoleOptions
}

func (*AlterRole) statement() {}

func (*CreateRole) statement() {}

type DropRole struct{ Names []string }

func (*DropRole) statement() {}

type SessionAuthorization struct {
	Name           string
	Default        bool
	OwnerDirective bool
}

func (*SessionAuthorization) statement() {}

type RoleSelection struct {
	Name  string
	Reset bool
	Pos   Position
}

func (*RoleSelection) statement() {}

type RoleField uint16

const (
	RoleSuperuser RoleField = 1 << iota
	RoleCreateDB
	RoleCreateRole
	RoleLogin
	RoleInherit
	RoleReplication
	RoleBypassRLS
	RoleConnectionLimit
	RolePassword
	RoleValidUntil
)

type RoleOptions struct {
	Present                                                                 RoleField
	Superuser, CreateDB, CreateRole, Login, Inherit, Replication, BypassRLS bool
	ConnectionLimit                                                         int32
	Password                                                                string
	PasswordNull                                                            bool
	ValidUntil                                                              string
	Duplicate                                                               Position
	Positions                                                               [10]Position // First occurrence, indexed by the RoleField bit.
	InitialMemberships                                                      []InitialRoleMembership
}

func (s RoleOptions) add(field RoleField, pos Position) RoleOptions {
	if s.Present&field != 0 && s.Duplicate == (Position{}) {
		s.Duplicate = pos
	}
	if s.Present&field == 0 {
		for i := range len(s.Positions) {
			if field == RoleField(1)<<i {
				s.Positions[i] = pos
				break
			}
		}
	}
	s.Present |= field
	return s
}

func (s RoleOptions) members(kind string, tok lexeme, members []RoleSpec) RoleOptions {
	for _, option := range s.InitialMemberships {
		if option.Kind == kind && s.Duplicate == (Position{}) {
			s.Duplicate = tok.pos
			break
		}
	}
	s.InitialMemberships = append(s.InitialMemberships, InitialRoleMembership{Kind: kind, Pos: tok.pos, Members: members, raw: tok.raw})
	return s
}

func (s RoleOptions) alter(sqllex sqlLexer) bool {
	if len(s.InitialMemberships) == 0 {
		return true
	}
	option := s.InitialMemberships[0]
	sqllex.(*lexer).syntax(lexeme{raw: option.raw, pos: option.Pos})
	return false
}

func (s RoleOptions) toggle(tok lexeme) (RoleOptions, bool) {
	word := tok.text
	value := true
	if len(word) > 2 && word[:2] == "no" {
		word = word[2:]
		value = false
	}
	var field RoleField
	switch word {
	case "superuser":
		field = RoleSuperuser
		s.Superuser = value
	case "createdb":
		field = RoleCreateDB
		s.CreateDB = value
	case "createrole":
		field = RoleCreateRole
		s.CreateRole = value
	case "login":
		field = RoleLogin
		s.Login = value
	case "inherit":
		field = RoleInherit
		s.Inherit = value
	case "replication":
		field = RoleReplication
		s.Replication = value
	case "bypassrls":
		field = RoleBypassRLS
		s.BypassRLS = value
	default:
		return s, false
	}
	return s.add(field, tok.pos), true
}

type Reset struct {
	Name Identifier
	All  bool
	Pos  Position
}

func (*Reset) statement() {}

type SettingCall struct {
	Name   string
	Args   []Expr
	Pos    Position
	Schema *Identifier
}

func (*SettingCall) statement() {}

type CreateSchema struct {
	Name                  string
	Authorization         string
	AuthorizationIdentity bool
	IfNotExists           bool
	Tables                []*CreateTable
}

func (*CreateSchema) statement() {}

type AlterSchema struct {
	Name          string
	NewName       string
	Owner         string
	OwnerIdentity bool
}

func (*AlterSchema) statement() {}

type DropSchema struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

func (*DropSchema) statement() {}

type settingList struct {
	values       []string
	stringValues []bool
}
type settingAtom struct {
	text     string
	isString bool
}

func settingIdentifier(value string) string {
	safe := value != ""
	for i := range len(value) {
		c := value[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			safe = false
			break
		}
	}
	if keyword := lookupKeyword(value); keyword != nil && keyword.category != Unreserved {
		safe = false
	}
	if safe {
		return value
	}
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}
func parsedSet(name Identifier, list settingList, pos Position) *Set {
	values := list.values
	if strings.EqualFold(name.Text, "search_path") {
		for i, isString := range list.stringValues {
			if isString {
				values[i] = settingIdentifier(values[i])
			}
		}
	}
	return &Set{Name: name, Values: values, Pos: pos}
}
