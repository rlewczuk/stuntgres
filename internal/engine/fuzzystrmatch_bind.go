package engine

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// fuzzystrmatchKernel is one installed fuzzystrmatch function group's
// overload selection, formal coercion and node construction.
type fuzzystrmatchKernel struct {
	input     func(name string, args []functions.Argument, text, integer sqltype.Usage) functions.Resolution
	signature func(name string, count int) string
	coerce    func(b functions.ArgumentCoercer, args []functions.Argument, text, integer sqltype.Usage) ([]expr.Expr, error)
	result    func(name string) sqltype.ID
	build     func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error)
}

var levenshteinKernel = fuzzystrmatchKernel{
	input:     functions.LevenshteinInput,
	signature: functions.LevenshteinSignature,
	coerce:    functions.CoerceLevenshteinArguments,
	result:    func(string) sqltype.ID { return sqltype.Int4 },
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return functions.NewLevenshtein(info, name, args)
	},
}

var metaphoneKernel = fuzzystrmatchKernel{
	input:     functions.MetaphoneInput,
	signature: functions.MetaphoneSignature,
	coerce:    functions.CoerceMetaphoneArguments,
	result:    func(string) sqltype.ID { return sqltype.Text },
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return functions.NewMetaphone(info, name, args)
	},
}

var soundexKernel = fuzzystrmatchKernel{
	input: func(name string, args []functions.Argument, text, _ sqltype.Usage) functions.Resolution {
		return functions.SoundexInput(name, args, text)
	},
	signature: functions.SoundexSignature,
	coerce: func(b functions.ArgumentCoercer, args []functions.Argument, text, _ sqltype.Usage) ([]expr.Expr, error) {
		return functions.CoerceSoundexArguments(b, args, text)
	},
	result: func(name string) sqltype.ID {
		if name == "difference" {
			return sqltype.Int4
		}
		return sqltype.Text
	},
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return functions.NewSoundex(info, name, args)
	},
}

// fuzzystrmatchKernelFor selects the group by the folded name, as the
// engine's chain did; the kernels see the lowercased spelling.
func fuzzystrmatchKernelFor(name string) fuzzystrmatchKernel {
	switch {
	case strings.EqualFold(name, "levenshtein") || strings.EqualFold(name, "levenshtein_less_equal"):
		return levenshteinKernel
	case strings.EqualFold(name, "metaphone") || strings.EqualFold(name, "dmetaphone") || strings.EqualFold(name, "dmetaphone_alt"):
		return metaphoneKernel
	}
	return soundexKernel
}

// bindFuzzystrmatch binds installed Levenshtein, Metaphone and Soundex calls.
// They sat before the shared gates, so they own labels and decorations.
func bindFuzzystrmatch(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []functions.Argument
	if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.CallLabelError(raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kernel, canonical := fuzzystrmatchKernelFor(name), strings.ToLower(name)
	routine, err := b.InstalledRoutine(raw.Name, functions.FuzzystrmatchExtension, kernel.signature(canonical, len(operands)))
	if err != nil {
		return nil, err
	}
	text, integer := b.Type(sqltype.Text).Usage(), b.Type(sqltype.Int4).Usage()
	if kernel.input(canonical, operands, text, integer) != functions.Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star || routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	args, err := kernel.coerce(b, operands, text, integer)
	if err != nil {
		return nil, err
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	call, err := kernel.build(b.Info(b.Type(kernel.result(canonical)).Usage(), raw.Pos, properties), canonical, args)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}

// installedArgumentNames spells analyzed arguments for 42883 diagnostics.
func installedArgumentNames(b functions.Binder, raw parser.FunctionExpr, operands []functions.Argument) []string {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = functions.NamedArgumentType(raw, i, names[i])
	}
	return names
}
