package parser

// CreateFunction is the bounded SQL text-body declaration. Body parameters are
// private routine inputs, never parameters of the enclosing CREATE statement.
type CreateFunction struct {
	Name      ObjectName
	Arguments []GrantRoutineArgument
	Result    TypeName
	Language  Identifier
	Body      string
	BodyPos   Position
	Pos       Position
}

func (*CreateFunction) statement() {}
