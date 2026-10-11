package parser

func stringSplitFunction(name string) bool {
	return name == "split_part" || name == "string_to_array" || name == "string_to_table"
}

// FunctionTable retains ordinary SQL spelling and lexical argument ownership.
// Only string_to_table is executable as a table source.
type FunctionTable struct {
	Calls                         []FunctionExpr
	Ordinality, Lateral, RowsFrom bool
	Pos                           Position
}

func (*FunctionTable) fromItem() {}
