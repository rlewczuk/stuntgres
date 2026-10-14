package parser

func stringSplitFunction(name string) bool {
	return name == "split_part" || name == "string_to_array" || name == "string_to_table"
}

// FunctionTable retains ordinary SQL spelling and lexical argument ownership.
// Binding admits string_to_table, json_object_keys and selected regexp functions;
// all-selected regexp syntax retains FunctionFrom, while mixed groups use this AST.
type FunctionTable struct {
	Calls                         []FunctionExpr
	Ordinality, Lateral, RowsFrom bool
	Pos                           Position
}

func (*FunctionTable) fromItem() {}
