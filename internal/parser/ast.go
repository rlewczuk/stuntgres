package parser

import "strings"

// Statement is the closed set of supported statements.
type Statement interface{ statement() }

// TypeName is syntax, not a resolved catalog type.
type TypeName struct {
	Name      string
	Pos       Position
	Quoted    bool
	Schema    *Identifier
	Modifiers []TypeModifier
}

// TypeModifier retains constant syntax until descriptor-owned resolution.
type TypeModifier struct {
	Integer   *int32
	Raw       string
	UnaryPlus bool
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
	Pos         Position
}

type ColumnConstraint struct {
	NotNull bool
	Default Expr
	Name    Identifier
	Pos     Position
}

type NotNullConstraint struct {
	Column Identifier
	Name   Identifier
	Pos    Position
}
type CreateTable struct {
	Name        ObjectName
	Elements    []TableElement
	Pos         Position
	IfNotExists bool
}

func (*CreateTable) statement() {}

type ViewOption struct {
	Name  Identifier
	Value string
}
type CreateView struct {
	Name    ObjectName
	Columns []Identifier
	Options []ViewOption
	Query   *Select
	Pos     Position
}

func (*CreateView) statement() {}

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

type TimeZoneExpr struct {
	Value, Zone Expr
	Local       bool
	Pos         Position
}

func (TimeZoneExpr) expression()                {}
func (e TimeZoneExpr) SourcePosition() Position { return e.Pos }

type TableElement struct {
	Column  *Column
	Like    *TableLike
	NotNull *NotNullConstraint
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

type AlterTableDropColumn struct {
	Name     ObjectName
	Column   Identifier
	IfExists bool
	Mode     DropMode
	Only     bool
	Pos      Position
}

func (*AlterTableDropColumn) statement() {}

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

type NotNullAction uint8

const (
	SetNotNull NotNullAction = iota + 1
	DropNotNull
	AddNotNull
	DropConstraint
)

type AlterTableNotNull struct {
	Name       ObjectName
	Column     Identifier
	Constraint Identifier
	Action     NotNullAction
	IfExists   bool
	Only       bool
	Mode       DropMode
	Pos        Position
}

func (*AlterTableNotNull) statement() {}

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
}

type InsertTarget struct {
	Name string
	Pos  Position
}

func (*Insert) statement() {}

// Undecorated VALUES stays destination-context input, even in parentheses.
// Only decorated VALUES is represented as a query source.
type InsertSource struct {
	Select *Select
	Rows   [][]Expr
	Order  *Order
}

func insertStatement(name ObjectName, targets []InsertTarget, source *InsertSource, pos Position) *Insert {
	stmt := &Insert{Name: name, Targets: targets, Pos: pos}
	if source.Select == nil && source.Order == nil {
		stmt.Rows = source.Rows
	} else {
		stmt.Source = source
	}
	return stmt
}

type Delete struct {
	Name  ObjectName
	Alias Identifier
	Where Expr
	Pos   Position
}

func (*Delete) statement() {}

type Update struct {
	Name        ObjectName
	Assignments []UpdateAssignment
	Where       Expr
	Pos         Position
}
type UpdateAssignment struct {
	Target Identifier
	Value  Expr
}

func (*Update) statement() {}

type Select struct {
	From    FromItem
	Targets []Expr
	Labels  []Identifier
	Where   Expr
	GroupBy []Expr
	Having  Expr
	Order   *Order
	Pos     Position
}

func (*Select) statement() {}

// IsSingleton admits only selected-call projections without a relation.
func (s *Select) IsSingleton() bool {
	if s.From != nil || s.Where != nil || len(s.GroupBy) != 0 || s.Having != nil || s.Order != nil || len(s.Targets) == 0 {
		return false
	}
	for _, target := range s.Targets {
		if _, star := target.(StarExpr); star {
			return false
		}
		if containsAggregate(target) {
			return false
		}
	}
	if sequenceProjection(s) {
		return true
	}
	for _, target := range s.Targets {
		call, ok := target.(FunctionExpr)
		if !ok || !singletonArgument(call) {
			return false
		}
	}
	return true
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
	case LiteralExpr, TypedLiteralExpr, ParameterExpr:
		return true
	case CastExpr:
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
		switch e.Name.Parts[len(e.Name.Parts)-1].Text {
		case "num_nulls", "num_nonnulls", "current_schemas", "random", "random_normal", "setseed", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod":
		default:
			return false
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

type LockTable struct {
	Name   ObjectName
	Pos    Position
	Nowait bool
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
	Isolation   TransactionIsolation
	AccessSet   bool
	ReadOnly    bool
	Unsupported bool
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

type FunctionExpr struct {
	Name     ObjectName
	Args     []Expr
	Pos      Position
	Variadic bool
}

// AggregateExpr distinguishes count-star from scalar calls and argument lists.
type AggregateExpr struct {
	Name   string
	Star   bool
	Schema *Identifier
	Pos    Position
}

func (AggregateExpr) expression()                {}
func (e AggregateExpr) SourcePosition() Position { return e.Pos }

func containsAggregate(expr Expr) bool {
	switch e := expr.(type) {
	case AggregateExpr:
		return true
	case CaseExpr:
		if containsAggregate(e.Test) || containsAggregate(e.Else) {
			return true
		}
		for _, arm := range e.Arms {
			if containsAggregate(arm.Condition) || containsAggregate(arm.Result) {
				return true
			}
		}
	case ConditionalExpr:
		for _, arg := range e.Args {
			if containsAggregate(arg) {
				return true
			}
		}
	case CastExpr:
		return containsAggregate(e.Child)
	case TimeZoneExpr:
		return containsAggregate(e.Value) || !e.Local && containsAggregate(e.Zone)
	case RegclassExpr:
		return containsAggregate(e.Child)
	case FunctionExpr:
		for _, arg := range e.Args {
			if containsAggregate(arg) {
				return true
			}
		}
	case CallExpr:
		for _, arg := range e.Args {
			if containsAggregate(arg) {
				return true
			}
		}
	case OperatorExpr:
		for _, arg := range e.Args {
			if containsAggregate(arg) {
				return true
			}
		}
	case PredicateExpr:
		return containsAggregate(e.Left) || containsAggregate(e.Right)
	case LogicalExpr:
		return containsAggregate(e.Left) || containsAggregate(e.Right)
	}
	return false
}

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
	if cast, ok := expr.(CastExpr); ok && cast.Syntax == PostfixCast {
		return ExpressionStart(cast.Child)
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
	Login        bool
	OptionCount  int
	SecondOption Position
}

func (*CreateRole) statement() {}

type DropRole struct{ Names []string }

func (*DropRole) statement() {}

type SessionAuthorization struct {
	Name           string
	Default        bool
	OwnerDirective bool
}

func (*SessionAuthorization) statement() {}

type IdentitySelect struct {
	Names  []string
	Schema *Identifier
	Pos    Position
}

func (*IdentitySelect) statement() {}

type roleOptionState struct {
	count  int
	login  bool
	second Position
}

func (s roleOptionState) add(tok lexeme, login bool) roleOptionState {
	s.count++
	if s.count == 1 {
		s.login = login
	}
	if s.count == 2 {
		s.second = tok.pos
	}
	return s
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
