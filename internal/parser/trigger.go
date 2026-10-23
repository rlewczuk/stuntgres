package parser

// CreateTrigger retains declaration syntax; semantic admission belongs to the
// catalog owner, including the timing and explicit row-kind boundary.
type CreateTrigger struct {
	Name      Identifier
	Table     ObjectName
	Replace   bool
	Timing    string
	Row       bool
	Events    []TriggerEvent
	When      Expr
	Function  ObjectName
	Arguments []string
	Pos       Position
}

type TriggerEvent struct {
	Name    string
	Columns []Identifier
}

func (*CreateTrigger) statement()                 {}
func (s *CreateTrigger) SourcePosition() Position { return s.Pos }
