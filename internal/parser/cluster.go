package parser

// Cluster names one logical heap and optional table-local index. An absent
// table selects the current database's remembered clustering preferences.
type Cluster struct {
	Table ObjectName
	Index Identifier
	Pos   Position
}

func (*Cluster) statement() {}
