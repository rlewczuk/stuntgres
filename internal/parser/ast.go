package parser

// Statement is the closed set of supported statements.
type Statement interface{ statement() }

type Type uint8

const (
	Int4 Type = iota
	Int8
	Text
	Bool
)

func (t Type) SQL() string          { return [...]string{"INTEGER", "BIGINT", "TEXT", "BOOLEAN"}[t] }
func (t Type) DatabaseType() string { return [...]string{"INT4", "INT8", "TEXT", "BOOL"}[t] }

type Position struct{ Offset, Character int }
type Column struct {
	Name     string
	Type     Type
	Nullable bool
	Pos      Position
}
type CreateTable struct {
	Name    string
	Columns []Column
	Pos     Position
}

func (*CreateTable) statement() {}

type Insert struct {
	Name    string
	Targets []InsertTarget
	Rows    [][]Value
	Pos     Position
}

type InsertTarget struct {
	Name string
	Pos  Position
}

func (*Insert) statement() {}

type Select struct {
	Name    string
	Star    bool
	Columns []string
	Where   *Predicate
	Order   *Order
	Pos     Position
}

func (*Select) statement() {}

type Predicate struct {
	Column string
	Value  Value
}
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
	ParameterValue
)

type Value struct {
	Kind      ValueKind
	Integer   int64
	Text      string
	Boolean   bool
	Parameter int
	Pos       Position
}
