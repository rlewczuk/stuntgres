package parser

// CreateStatistics retains source order for generated names; the catalog owns
// canonical stable attribute identities, never these parser slices.
type CreateStatistics struct {
	Name        ObjectName
	Relation    ObjectName
	Elements    []Expr
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

type AlterStatisticsTarget struct {
	Name     ObjectName
	Target   int64
	IfExists bool
	Pos      Position
}

func (*AlterStatisticsTarget) statement()                 {}
func (s *AlterStatisticsTarget) SourcePosition() Position { return s.Pos }

// Identity alterations retain the original definition and collected state.
type AlterStatisticsRename struct {
	Name    ObjectName
	NewName Identifier
	Pos     Position
}
type AlterStatisticsSchema struct {
	Name   ObjectName
	Schema Identifier
	Pos    Position
}

func (*AlterStatisticsRename) statement()                 {}
func (*AlterStatisticsSchema) statement()                 {}
func (s *AlterStatisticsRename) SourcePosition() Position { return s.Pos }
func (s *AlterStatisticsSchema) SourcePosition() Position { return s.Pos }
