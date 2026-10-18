package functions

import (
	"stuntgres/internal/parser"
)

// analyzeArrayArguments analyzes ordinary arguments as comparison operands,
// then applies the shared FILTER/count/label envelope.
func analyzeArrayArguments(b Binder, raw parser.FunctionExpr) ([]Argument, error) {
	var small [3]Argument
	var operands []Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	return operands, nil
}

// arraySignatureError reports the missing signature with array-aware,
// labelled argument type names.
func arraySignatureError(b Binder, raw parser.FunctionExpr, operands []Argument) error {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = ArrayTypeName(b, d)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	return b.SignatureError(raw.Name, raw.Pos, names)
}

func arrayNamespace(name parser.ObjectName) string {
	if len(name.Parts) == 2 {
		return name.Parts[0].Text
	}
	return ""
}
