package parser

// CreateStatistics retains source order for generated names; the catalog owns
// canonical stable attribute identities, never these parser slices.
type CreateStatistics struct {
	Name        ObjectName
	Relation    ObjectName
	Columns     []Identifier
	Kinds       []Identifier
	IfNotExists bool
	Pos         Position
}
type DropStatistics struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*CreateStatistics) statement()                 {}
func (*DropStatistics) statement()                   {}
func (s *CreateStatistics) SourcePosition() Position { return s.Pos }
func (s *DropStatistics) SourcePosition() Position   { return s.Pos }
