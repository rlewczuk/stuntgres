package functions

import (
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// daitch_mokotoff binds with unaccent (bindInstalledText).
var fuzzystrmatchNames = [...]string{"levenshtein", "levenshtein_less_equal", "metaphone", "dmetaphone", "dmetaphone_alt", "soundex", "difference"}

// fuzzystrmatchKernel is one installed fuzzystrmatch function group's
// overload selection, formal coercion and node construction.
type fuzzystrmatchKernel struct {
	input     func(name string, args []Argument, text, integer sqltype.Usage) Resolution
	signature func(name string, count int) string
	coerce    func(b ArgumentCoercer, args []Argument, text, integer sqltype.Usage) ([]expr.Expr, error)
	result    func(name string) sqltype.ID
	build     func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error)
}

var levenshteinKernel = fuzzystrmatchKernel{
	input:     LevenshteinInput,
	signature: LevenshteinSignature,
	coerce:    CoerceLevenshteinArguments,
	result:    func(string) sqltype.ID { return sqltype.Int4 },
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return NewLevenshtein(info, name, args)
	},
}

var metaphoneKernel = fuzzystrmatchKernel{
	input:     MetaphoneInput,
	signature: MetaphoneSignature,
	coerce:    CoerceMetaphoneArguments,
	result:    func(string) sqltype.ID { return sqltype.Text },
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return NewMetaphone(info, name, args)
	},
}

var soundexKernel = fuzzystrmatchKernel{
	input: func(name string, args []Argument, text, _ sqltype.Usage) Resolution {
		return SoundexInput(name, args, text)
	},
	signature: SoundexSignature,
	coerce: func(b ArgumentCoercer, args []Argument, text, _ sqltype.Usage) ([]expr.Expr, error) {
		return CoerceSoundexArguments(b, args, text)
	},
	result: func(name string) sqltype.ID {
		if name == "difference" {
			return sqltype.Int4
		}
		return sqltype.Text
	},
	build: func(info expr.Info, name string, args []expr.Expr) (expr.Expr, error) {
		return NewSoundex(info, name, args)
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
func bindFuzzystrmatch(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []Argument
	if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
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
	if err := CallLabelError(raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kernel, canonical := fuzzystrmatchKernelFor(name), strings.ToLower(name)
	routine, err := b.InstalledRoutine(raw.Name, FuzzystrmatchExtension, kernel.signature(canonical, len(operands)))
	if err != nil {
		return nil, err
	}
	text, integer := b.Type(sqltype.Text).Usage(), b.Type(sqltype.Int4).Usage()
	if kernel.input(canonical, operands, text, integer) != Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star || routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	args, err := kernel.coerce(b, operands, text, integer)
	if err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
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
