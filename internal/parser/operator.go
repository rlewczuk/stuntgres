package parser

// Operator signatures preserve ordered arguments; NONE denotes a prefix slot.
type OperatorSignature struct {
	Name        ObjectName
	Left, Right TypeName
	Pos         Position
}

// OperatorOptionArgumentKind preserves PostgreSQL definition-element node kinds.
type OperatorOptionArgumentKind uint8

const (
	OperatorOptionNoArgument OperatorOptionArgumentKind = iota
	OperatorOptionTypeArgument
	OperatorOptionNameArgument
	OperatorOptionStringArgument
	OperatorOptionIntegerArgument
	OperatorOptionFloatArgument
	OperatorOptionNoneArgument
)

type OperatorOption struct {
	Name      string
	HasValue  bool
	Kind      OperatorOptionArgumentKind
	Type      TypeName
	Reference ObjectName
	Value     string
	Pos       Position
	ValuePos  Position
}

func operatorTypeOption(name lexeme, typ TypeName) OperatorOption {
	if len(typ.Parts) == 0 && typ.Schema == nil && !typ.Quoted {
		alias := standaloneType(typ)
		if alias.System {
			typ.System = true
			typ.Parts = []Identifier{{Text: "pg_catalog", Pos: typ.Pos}, {Text: alias.Name, Pos: typ.Pos}}
		}
	}
	kind := OperatorOptionTypeArgument
	if typ.Name == "none" && !typ.Quoted && typ.Schema == nil && !typ.Setof && len(typ.Modifiers) == 0 && len(typ.ArrayBounds) == 0 {
		kind = OperatorOptionNoneArgument
	}
	return OperatorOption{Name: name.text, HasValue: true, Kind: kind, Type: typ, Pos: name.pos, ValuePos: typ.Pos}
}

func operatorNumericOption(name, number lexeme, sign string, pos Position) OperatorOption {
	// The scanner's unsigned token must fit int32 before NumericOnly applies
	// its sign. In particular, -2147483648 is a Float node, not an Integer.
	value := numberValue(number, "")
	kind := OperatorOptionFloatArgument
	if value.Kind == IntegerValue && value.Integer <= 1<<31-1 {
		kind = OperatorOptionIntegerArgument
	}
	return OperatorOption{Name: name.text, HasValue: true, Kind: kind, Value: sign + number.text, Pos: name.pos, ValuePos: pos}
}

type CreateOperator struct {
	Name    ObjectName
	Options []OperatorOption
	Pos     Position
}

func (*CreateOperator) statement() {}

type AlterOperator struct {
	Signature     OperatorSignature
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Options       []OperatorOption
	Pos           Position
}

func (*AlterOperator) statement() {}

type DropOperator struct {
	Signatures []OperatorSignature
	IfExists   bool
	Mode       DropMode
	Pos        Position
}

func (*DropOperator) statement() {}
