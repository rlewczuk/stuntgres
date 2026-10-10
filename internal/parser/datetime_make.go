package parser

import "strings"

func datetimeMakeFunctionName(name string) bool {
	switch strings.ToLower(name) {
	case "make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval":
		return true
	}
	return false
}

func appendFunctionArgument(call FunctionExpr, arg Expr, label Identifier) FunctionExpr {
	if label.Text != "" && call.ArgNames == nil {
		call.ArgNames = make([]Identifier, len(call.Args), len(call.Args)+1)
	}
	call.Args = append(call.Args, arg)
	if call.ArgNames != nil {
		call.ArgNames = append(call.ArgNames, label)
	}
	return call
}
