package parser

type EventTriggerFilter struct {
	Name   Identifier
	Values []string
}

type CreateEventTrigger struct {
	Name     Identifier
	Event    Identifier
	Filters  []EventTriggerFilter
	Function ObjectName
	Pos      Position
}

func (*CreateEventTrigger) statement()                 {}
func (s *CreateEventTrigger) SourcePosition() Position { return s.Pos }

type AlterEventTrigger struct {
	Name          Identifier
	Action        string
	FiringMode    byte
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}

func (*AlterEventTrigger) statement()                 {}
func (s *AlterEventTrigger) SourcePosition() Position { return s.Pos }

type DropEventTrigger struct {
	Name     Identifier
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropEventTrigger) statement()                 {}
func (s *DropEventTrigger) SourcePosition() Position { return s.Pos }
