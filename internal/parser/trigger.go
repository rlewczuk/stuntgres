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
	token   lexeme
}

func appendTriggerEvent(l *lexer, events []TriggerEvent, event TriggerEvent) ([]TriggerEvent, bool) {
	for _, previous := range events {
		if previous.Name == event.Name {
			l.failAt("42601", "duplicate trigger events specified at or near \""+event.token.raw+"\"", event.token.pos.Offset)
			return nil, false
		}
	}
	return append(events, event), true
}

func (*CreateTrigger) statement()                 {}
func (s *CreateTrigger) SourcePosition() Position { return s.Pos }
