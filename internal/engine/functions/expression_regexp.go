package functions

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type RegexpContext interface {
	Context
	FunctionRegexExecution() *sqltype.PGRegexExecution
}
type RegexpCall[E Expression[C], C RegexpContext, P any] struct {
	Info[P]
	Plan         sqltype.RegexpPlan
	Args         []E
	WrittenSlots [7]uint8
	Labels       [7]string
}
type Regexp[E Expression[C], C RegexpContext, P any] struct{ RegexpCall[E, C, P] }
type RegexpSet[E Expression[C], C RegexpContext, P any] struct{ RegexpCall[E, C, P] }

func (call RegexpCall[E, C, P]) evaluateArguments(c C) ([7]sqltype.Value, error) {
	var values [7]sqltype.Value
	if _, err := evaluationContext(c); err != nil {
		return values, err
	}
	for _, slot := range call.WrittenSlots[:len(call.Args)] {
		value, err := call.Args[slot].Eval(c)
		if err != nil {
			c.FunctionRegexExecution().Reset()
			return values, err
		}
		values[slot] = value
	}
	return values, nil
}
func (e Regexp[E, C, P]) Eval(c C) (sqltype.Value, error) {
	values, err := e.evaluateArguments(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Evaluate(c.FunctionContext(), values, c.FunctionRegexExecution())
}
func (e RegexpSet[E, C, P]) Eval(c C) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted regular expression set call")
}
func (e RegexpSet[E, C, P]) EvalSet(c C) ([]sqltype.Value, error) {
	values, err := e.evaluateArguments(c)
	if err != nil {
		return nil, err
	}
	return e.Plan.EvaluateSet(c.FunctionContext(), values, c.FunctionRegexExecution())
}
func NewRegexpCall[E Expression[C], C RegexpContext, P any](info Info[P], name string, args []E, writtenSlots [7]uint8, labels [7]string, collation sqltype.CollationRef, conflict bool) (RegexpCall[E, C, P], error) {
	usages := make([]sqltype.Usage, len(args))
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindRegexpFunction(name, usages, info.Usage, collation, conflict)
	return RegexpCall[E, C, P]{info, plan, args, writtenSlots, labels}, err
}
func (call RegexpCall[E, C, P]) Reconstruct(info Info[P], args []E, collation sqltype.CollationRef, conflict bool) (RegexpCall[E, C, P], error) {
	return NewRegexpCall[E, C](info, call.Plan.Name(), args, call.WrittenSlots, call.Labels, collation, conflict)
}
func RegexpSignature(name string, n int, replaceFlags bool) (labels [7]string, types [7]sqltype.ID, result sqltype.ID, valid bool) {
	labels = [7]string{"string", "pattern"}
	types = [7]sqltype.ID{sqltype.Text, sqltype.Text}
	result = sqltype.Text
	valid = n >= 2 && n <= 7
	switch name {
	case "regexp_count":
		valid = valid && n <= 4
		labels[2], labels[3] = "start", "flags"
		types[2], types[3] = sqltype.Int4, sqltype.Text
		result = sqltype.Int4
	case "regexp_instr":
		labels = [7]string{"string", "pattern", "start", "N", "endoption", "flags", "subexpr"}
		types = [7]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Int4, sqltype.Text, sqltype.Int4}
		result = sqltype.Int4
	case "regexp_substr":
		valid = valid && n <= 6
		labels = [7]string{"string", "pattern", "start", "N", "flags", "subexpr"}
		types = [7]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Text, sqltype.Int4}
	case "regexp_like", "regexp_match", "regexp_matches", "regexp_split_to_array", "regexp_split_to_table":
		valid = valid && n <= 3
		labels[2] = "flags"
		types[2] = sqltype.Text
		if name == "regexp_like" {
			result = sqltype.Bool
		}
		if name == "regexp_match" || name == "regexp_matches" || name == "regexp_split_to_array" {
			result = 1009
		}
	case "regexp_replace":
		valid = valid && n >= 3 && n <= 6
		labels = [7]string{"string", "pattern", "replacement", "start", "N", "flags"}
		types = [7]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Int4, sqltype.Text}
		if n == 4 && replaceFlags {
			labels[3] = "flags"
			types[3] = sqltype.Text
		}
	default:
		valid = false
	}
	return
}

func RegexpReplaceSelection[E any](raw parser.FunctionExpr, args []Argument[E]) (flags bool, probe *Argument[E]) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "regexp_replace" || len(args) != 4 {
		return true, nil
	}
	for i, argument := range args {
		if len(raw.ArgNames) > i && raw.ArgNames[i].Text == "start" {
			return false, nil
		}
		if i == 3 && (len(raw.ArgNames) == 0 || raw.ArgNames[i].Text == "") && argument.Usage.Type != nil {
			return true, &args[i]
		}
	}
	return true, nil
}
func RegexpReplaceFlags(source, integer sqltype.Usage) bool {
	_, err := sqltype.PlanCoercion(source, integer, sqltype.Implicit)
	return err != nil
}
func RegexpSlots(raw parser.FunctionExpr, roster [7]string, count int) (written [7]uint8, labels [7]string, valid bool) {
	var supplied [7]bool
	for i := 0; i < count; i++ {
		slot := i
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range roster[:count] {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
		}
		if slot < 0 || supplied[slot] {
			return written, labels, false
		}
		supplied[slot] = true
		written[i] = uint8(slot)
		if len(raw.ArgNames) > 0 {
			labels[i] = raw.ArgNames[i].Text
		}
	}
	return written, labels, true
}
func RegexpCompatible(source, target sqltype.Usage) bool {
	if source.Type == nil {
		return true
	}
	var err error
	if target.Type.ID() == sqltype.Text && source.Type.BaseType().ID() == sqltype.Char {
		_, err = sqltype.PlanExtremaCharText(source, target)
	} else {
		_, err = sqltype.PlanCoercion(source, target, sqltype.Implicit)
	}
	return err == nil
}
func CoerceRegexpArguments[E any, B ArgumentCoercer[E]](b B, operands []Argument[E], written [7]uint8, targets [7]sqltype.Usage) ([]E, error) {
	args := make([]E, len(operands))
	for i, operand := range operands {
		slot := written[i]
		target := targets[slot]
		intent := ExactFormalArgument
		if target.Type.ID() == sqltype.Text {
			intent = CollatedTextFormalArgument
			if operand.Usage.Collation.ID != 0 {
				target.Collation = operand.Usage.Collation
			}
		}
		argument, err := b.CoerceArgument(operand, CoercionRequest{Intent: intent, Target: target})
		if err != nil {
			return nil, err
		}
		args[slot] = argument
	}
	return args, nil
}
func RegexpStageDependent(name string) bool {
	kind := sqltype.RegexpFunctionKind(name)
	return kind == sqltype.RegexpMatches || kind == sqltype.RegexpSplitTable
}
