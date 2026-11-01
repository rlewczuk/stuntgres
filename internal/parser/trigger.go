package parser

// CreateTrigger retains declaration syntax; semantic admission belongs to the
// catalog owner, including the timing and explicit row-kind boundary.
type CreateTrigger struct {
	Name              Identifier
	Table             ObjectName
	Replace           bool
	Constraint        bool
	From              ObjectName
	FromPos           Position
	Deferrable        bool
	InitiallyDeferred bool
	DeferrabilitySet  bool
	InitiallySet      bool
	DeferrabilityPos  Position
	InitiallyPos      Position
	Timing            string
	Row               bool
	Events            []TriggerEvent
	Transitions       []TriggerTransition
	When              Expr
	Function          ObjectName
	Arguments         []string
	Pos               Position
}

type TriggerEvent struct {
	Name    string
	Columns []Identifier
	token   lexeme
}

// TriggerTransition preserves declaration order for semantic diagnostic precedence.
// Row remains syntax here: the catalog owner rejects unsupported ROW naming.
type TriggerTransition struct {
	New   bool
	Row   bool
	Alias Identifier
	Pos   Position
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
func triggerProperty(l *lexer, s *CreateTrigger, token lexeme, deferrability, value bool) bool {
	if deferrability {
		s.DeferrabilityPos = token.pos
	} else {
		s.InitiallyPos = token.pos
	}
	return constraintProperty(l, token, deferrability, value, &s.Deferrable, &s.InitiallyDeferred, &s.DeferrabilitySet, &s.InitiallySet)
}

func (*CreateTrigger) statement()                 {}
func (s *CreateTrigger) SourcePosition() Position { return s.Pos }
