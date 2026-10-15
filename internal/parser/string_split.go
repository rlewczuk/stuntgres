package parser

func stringSplitFunction(name string) bool {
	return name == "split_part" || name == "string_to_array" || name == "string_to_table"
}

// FunctionTable retains ordinary SQL spelling and lexical argument ownership.
// Binding admits existing series, subscripts, string, JSON and regexp providers;
// all-selected regexp syntax retains FunctionFrom, while mixed groups use this AST.
type FunctionTable struct {
	Calls                         []FunctionExpr
	Ordinality, Lateral, RowsFrom bool
	Pos                           Position
}

func (*FunctionTable) fromItem() {}

// NormalizedFunctionCalls owns the PostgreSQL FROM-only multiargument UNNEST
// rewrite. Binder and symbolic renderer must share this ordering without
// mutating the retained prepared/view AST. Ordinary unary calls allocate nothing.
func NormalizedFunctionCalls(raw *FunctionTable) []FunctionExpr {
	var calls []FunctionExpr
	for i, call := range raw.Calls {
		rewrite := len(call.Name.Parts) == 1 && call.Name.Parts[0].Text == "unnest" && len(call.Args) > 1 && !call.Distinct && !call.Star && !call.Variadic && len(call.Order) == 0 && call.Filter == nil && call.Window == nil && !call.Within && len(call.ColumnDefinitions) == 0
		if rewrite {
			if calls == nil {
				calls = make([]FunctionExpr, 0, len(raw.Calls)+len(call.Args)-1)
				calls = append(calls, raw.Calls[:i]...)
			}
			for j, arg := range call.Args {
				unary := call
				unary.Name = ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: call.Pos}, call.Name.Parts[0]}}
				unary.Args = []Expr{arg}
				unary.ArgNames = nil
				if len(call.ArgNames) != 0 && call.ArgNames[j].Text != "" {
					unary.ArgNames = []Identifier{call.ArgNames[j]}
				}
				calls = append(calls, unary)
			}
		} else if calls != nil {
			calls = append(calls, call)
		}
	}
	if calls == nil {
		return raw.Calls
	}
	return calls
}
