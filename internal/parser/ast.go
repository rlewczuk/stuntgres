package parser

// Statement is the closed set of supported statements.
type Statement interface{ statement() }

// TypeName is syntax, not a resolved catalog type.
type TypeName struct {
	Name string
	Pos  Position
}

type Position struct{ Offset, Character int }
type Identifier struct {
	Text string
	Pos  Position
}
type ObjectName struct{ Parts []Identifier }
type Column struct {
	Name     string
	TypeName TypeName
	Nullable bool
	Pos      Position
}
type CreateTable struct {
	Name        ObjectName
	Columns     []Column
	Pos         Position
	IfNotExists bool
}

func (*CreateTable) statement() {}

type Insert struct {
	Name    ObjectName
	Targets []InsertTarget
	Rows    [][]Expr
	Pos     Position
}

type InsertTarget struct {
	Name string
	Pos  Position
}

func (*Insert) statement() {}

type Select struct {
	Name    ObjectName
	Star    bool
	Targets []Expr
	Where   Expr
	Order   *Order
	Pos     Position
}

func (*Select) statement() {}

type LockTable struct {
	Name ObjectName
	Pos  Position
}

func (*LockTable) statement() {}

type Order struct {
	Column string
	Desc   bool
}
type ValueKind uint8

const (
	NullValue ValueKind = iota
	IntegerValue
	StringValue
	BooleanValue
)

type Value struct {
	Kind      ValueKind
	Integer   int64
	Text      string
	Boolean   bool
	Pos       Position
}

// Expr is the closed set of admitted scalar syntax occurrences.
type Expr interface {
	expression()
	SourcePosition() Position
}
type LiteralExpr struct { Value Value }
type ColumnExpr struct { Name ObjectName; Pos Position }
type ParameterExpr struct { Number int; Pos Position }
type OperatorExpr struct { Name ObjectName; Args []Expr; Pos Position }
func (LiteralExpr) expression() {}
func (ColumnExpr) expression() {}
func (ParameterExpr) expression() {}
func (OperatorExpr) expression() {}
func (e LiteralExpr) SourcePosition() Position { return e.Value.Pos }
func (e ColumnExpr) SourcePosition() Position { return e.Pos }
func (e ParameterExpr) SourcePosition() Position { return e.Pos }
func (e OperatorExpr) SourcePosition() Position { return e.Pos }

// Set uses setting-specific scalar text; Default is distinct from quoted text.
type Set struct {
	Name    Identifier
	Default bool
	Values  []string
	Pos     Position
}

func (*Set) statement() {}

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

type IdentitySelect struct{ Names []string }

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
	Name string
	Args []Expr
	Pos  Position
}

func (*SettingCall) statement() {}
