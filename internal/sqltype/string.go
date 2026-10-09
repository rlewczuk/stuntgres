package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

// StringTextInput models only this string family's implicit TEXT candidates.
// Internal CHAR's release edge does not broaden public general cast admission.
func StringTextInput(id ID) bool { return IsCharacter(id) || id == Name || id == Char }
func StringCategory(id ID) bool  { return IsCharacter(id) || id == Name }

// PlanExtremaCharText is the family-local internal CHAR input conversion for
// min/max's TEXT overload. It deliberately does not add a general cast edge.
func PlanExtremaCharText(source, target Usage) (CoercionPlan, error) {
	if !source.Valid() || !target.Valid() || source.Type.BaseType().ID() != Char || target.Type.ID() != Text || target.Typmod != -1 {
		return CoercionPlan{}, fmt.Errorf("invalid extrema CHAR conversion")
	}
	p := CoercionPlan{source: source.BaseUsage(), target: target, context: Implicit, method: charTextConversion}
	if source.Type.IsDomain() {
		p.logicalSource, p.logicalTarget = source.Type, target.Type
	}
	return p, nil
}
func StringScalar(id ID) bool { return id != Unknown && id != NameArray && id != 0 }

// ConcatOutput is the release mixed wrapper's explicit-to-text output, not the
// driver export representation or a new public cast edge.
func (d *Descriptor) ConcatOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if d == nil || v.Type() != d || v.IsNull() {
		return "", fmt.Errorf("invalid concat output value")
	}
	if d.IsDomain() {
		v.typ = d.BaseType()
		return v.typ.ConcatOutput(ctx, v, env)
	}
	if d.id == Bool {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if v.boolean {
			return "true", nil
		}
		return "false", nil
	}
	if d.id == Regclass {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if env.Regclass == nil {
			return "", fmt.Errorf("missing regclass output snapshot")
		}
		id, err := strconv.ParseUint(v.text, 10, 32)
		if err != nil {
			return "", err
		}
		return env.Regclass.FormatRegclass(uint32(id))
	}
	return d.OutputWithEnvironment(ctx, v, env)
}

type StringPlan struct {
	name        string
	result      Usage
	text        Usage
	args        [4]Usage
	formal      [4]Usage
	count       uint8
	textual     [4]bool
	conversions [4]CoercionPlan
	conflict    bool
	caseOp      unicode16.CaseOperation
}

func (p StringPlan) Name() string                  { return p.name }
func (p StringPlan) Result() Usage                 { return p.result }
func (p StringPlan) TextUsage() Usage              { return p.text }
func (p StringPlan) ArgumentUsage(index int) Usage { return p.formal[index] }
func (p StringPlan) CollationIndeterminate() bool  { return p.conflict }

// SessionDependent describes the concrete conversions after the release's SQL
// concatenation wrapper specialization, independently of child dependence.
func (p StringPlan) SessionDependent() bool {
	for i := range int(p.count) {
		if p.textual[i] {
			if p.args[i].Type.ID() != Char && p.conversions[i].SessionDependent() {
				return true
			}
		} else if p.args[i].Type.OutputSessionDependent() {
			return true
		}
	}
	return false
}

// Non-TEXT results also receive the generation's TEXT owner, avoiding global
// descriptor lookup or per-evaluation descriptor allocation.
func BindString(name string, result, text Usage, args []Usage, textual [4]bool, conflict bool) (StringPlan, error) {
	p := StringPlan{name: name, result: result, text: text, count: uint8(len(args)), textual: textual, conflict: conflict}
	if !result.Valid() || !text.Valid() || text.Type.ID() != Text || len(args) < 1 || len(args) > 4 || result.Type.ID() == Text && result.Type != text.Type {
		return p, fmt.Errorf("invalid string signature")
	}
	switch name {
	case "||":
		if len(args) != 2 || result.Type.ID() != Text || !textual[0] && !textual[1] {
			return p, fmt.Errorf("invalid concatenation signature")
		}
	case "btrim", "ltrim", "rtrim", "normalize":
		if len(args) > 2 || result.Type.ID() != Text || !textual[0] || len(args) == 2 && !textual[1] {
			return p, fmt.Errorf("invalid string function signature")
		}
	case "lower", "upper", "casefold", "initcap":
		if len(args) != 1 || result.Type.ID() != Text || !textual[0] {
			return p, fmt.Errorf("invalid case function signature")
		}
		switch name {
		case "lower":
			p.caseOp = unicode16.Lower
		case "upper":
			p.caseOp = unicode16.Upper
		case "casefold":
			p.caseOp = unicode16.Fold
		case "initcap":
			p.caseOp = unicode16.Initcap
		}
	case "is_normalized":
		if len(args) > 2 || result.Type.ID() != Bool || !textual[0] || len(args) == 2 && !textual[1] {
			return p, fmt.Errorf("invalid normalized predicate signature")
		}
	case "length", "char_length", "character_length", "octet_length", "bit_length":
		if len(args) != 1 || result != result.Type.Usage() || result.Type.ID() != Int4 || !textual[0] {
			return p, fmt.Errorf("invalid string count signature")
		}
	case "position", "strpos":
		if len(args) != 2 || result != result.Type.Usage() || result.Type.ID() != Int4 || !textual[0] || !textual[1] {
			return p, fmt.Errorf("invalid string search signature")
		}
	case "overlay":
		if (len(args) != 3 && len(args) != 4) || result.Type.ID() != Text || !textual[0] || !textual[1] || textual[2] || textual[3] {
			return p, fmt.Errorf("invalid string overlay signature")
		}
	case "replace", "translate":
		if len(args) != 3 || result.Type.ID() != Text || !textual[0] || !textual[1] || !textual[2] {
			return p, fmt.Errorf("invalid string replacement signature")
		}
	case "starts_with":
		if len(args) != 2 || result != result.Type.Usage() || result.Type.ID() != Bool || !textual[0] || !textual[1] {
			return p, fmt.Errorf("invalid string prefix signature")
		}
	default:
		return p, fmt.Errorf("invalid string provider name")
	}
	for i, u := range args {
		if !u.Valid() {
			return p, fmt.Errorf("invalid string input usage")
		}
		p.args[i] = u
		target := text
		if name == "overlay" && i >= 2 {
			if u.Type.ID() != Int4 || u != u.Type.Usage() {
				return p, fmt.Errorf("invalid string overlay integer input")
			}
			p.formal[i] = u
			continue
		}
		if (name == "length" || name == "char_length" || name == "character_length" || name == "octet_length") && u.Type.BaseType().ID() == BPChar {
			target = u.Type.BaseType().Usage()
		}
		p.formal[i] = target
		if u.Type.BaseType().ID() == Text && u.Type.BaseType() != text.Type {
			return p, fmt.Errorf("foreign string text owner")
		}
		if textual[i] {
			if !StringTextInput(u.Type.BaseType().ID()) {
				return p, fmt.Errorf("invalid string text input")
			}
			if u.Type.ID() != Char {
				var err error
				p.conversions[i], err = PlanCoercion(u, target, Implicit)
				if err != nil {
					return p, err
				}
			}
		} else if !StringScalar(u.Type.BaseType().ID()) {
			return p, fmt.Errorf("invalid mixed string input")
		}
	}
	return p, nil
}
func (p StringPlan) Evaluate(ctx context.Context, values [4]Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for i := range int(p.count) {
		if values[i].Type() != p.args[i].Type {
			return Value{}, fmt.Errorf("foreign string input value")
		}
	}
	for i := range int(p.count) {
		if values[i].IsNull() {
			return Null(p.result.Type), nil
		}
	}
	var texts [4]string
	for i := range int(p.count) {
		v := values[i]
		if p.name == "overlay" && i >= 2 {
			continue
		}
		var err error
		// Selected BPCHAR-to-TEXT conversion uses the same type-owned edge,
		// but bounds its potentially long ASCII-padding scan before Apply.
		if v.Type().BaseType().ID() == BPChar && p.formal[i].Type.ID() == Text {
			v.text, err = trimStringPadding(ctx, v.text)
			if err != nil {
				return Value{}, err
			}
		}
		if p.textual[i] && v.Type().ID() != Char {
			v, err = p.conversions[i].Apply(ctx, v, input, output)
			if err != nil {
				return Value{}, err
			}
			texts[i], err = v.Text()
		} else {
			texts[i], err = v.Type().ConcatOutput(ctx, v, output)
		}
		if err != nil {
			return Value{}, err
		}
	}
	if p.result.Type.ID() == Int4 {
		return p.evaluateLength(ctx, texts)
	}
	if p.name == "overlay" || p.name == "replace" || p.name == "translate" || p.name == "starts_with" {
		return p.evaluateReplacement(ctx, texts, values)
	}
	if p.caseOp != 0 {
		if p.conflict {
			name := p.name
			// PostgreSQL18.0 casefold uses lower() in this diagnostic.
			if name == "casefold" {
				name = "lower"
			}
			return Value{}, &sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22",
				Message: "could not determine which collation to use for " + name + "() function",
				Hint:    "Use the COLLATE clause to set the collation explicitly.",
			}
		}
		mode := unicode16.ASCII
		switch p.result.Collation.ID {
		case 811:
			mode = unicode16.Simple
		case 6411:
			mode = unicode16.Full
		}
		text, err := unicode16.ConvertCase(ctx, texts[0], p.caseOp, mode)
		if err != nil {
			return Value{}, err
		}
		return String(p.result.Type, text), nil
	}
	var text string
	switch p.name {
	case "||":
		text = texts[0] + texts[1]
	case "btrim", "ltrim", "rtrim":
		set := " "
		if p.count == 2 {
			set = texts[1]
		}
		var err error
		text, err = trimRunes(ctx, texts[0], set, p.name)
		if err != nil {
			return Value{}, err
		}
	case "normalize", "is_normalized":
		spelling := "NFC"
		if p.count == 2 {
			spelling = texts[1]
		}
		form, ok := unicode16.ParseForm(spelling)
		if !ok {
			return Value{}, failure("22023", "invalid normalization form: "+spelling)
		}
		if p.name == "is_normalized" {
			yes, err := unicode16.IsNormalized(ctx, texts[0], form)
			if err != nil {
				return Value{}, err
			}
			return Boolean(p.result.Type, yes), nil
		}
		var err error
		text, err = unicode16.Normalize(ctx, texts[0], form)
		if err != nil {
			return Value{}, err
		}
	default:
		return Value{}, fmt.Errorf("invalid string operation")
	}
	return String(p.result.Type, text), nil
}
func trimRunes(ctx context.Context, source, set, direction string) (string, error) {
	if source == "" || set == "" {
		return source, ctx.Err()
	}
	var work uint32
	contains := func(want rune) (bool, error) {
		for _, r := range set {
			work++
			if work&255 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			if r == want {
				return true, nil
			}
		}
		return false, nil
	}
	start, end := 0, len(source)
	if direction != "rtrim" {
		for start < end {
			work++
			if work&255 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			r, n := utf8.DecodeRuneInString(source[start:end])
			member, err := contains(r)
			if err != nil {
				return "", err
			}
			if !member {
				break
			}
			start += n
		}
	}
	if direction != "ltrim" {
		for start < end {
			work++
			if work&255 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			r, n := utf8.DecodeLastRuneInString(source[start:end])
			member, err := contains(r)
			if err != nil {
				return "", err
			}
			if !member {
				break
			}
			end -= n
		}
	}
	return source[start:end], ctx.Err()
}
