package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

// StringTextInput models only this string family's implicit TEXT candidates.
// Internal CHAR's release edge does not broaden public general cast admission.
func StringTextInput(id ID) bool { return IsCharacter(id) || id == Name || id == Char }
func StringCategory(id ID) bool  { return IsCharacter(id) || id == Name }
func StringScalar(id ID) bool    { return id != Unknown && id != NameArray && id != 0 }

// ConcatOutput is the release mixed wrapper's explicit-to-text output, not the
// driver export representation or a new public cast edge.
func (d *Descriptor) ConcatOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if d == nil || v.Type() != d || v.IsNull() {
		return "", fmt.Errorf("invalid concat output value")
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
	args        [2]Usage
	count       uint8
	textual     [2]bool
	conversions [2]CoercionPlan
}

func (p StringPlan) Name() string     { return p.name }
func (p StringPlan) Result() Usage    { return p.result }
func (p StringPlan) TextUsage() Usage { return p.text }

// Bool-result predicates also receive the generation's TEXT owner, avoiding
// global descriptor lookup or per-evaluation descriptor allocation.
func BindString(name string, result, text Usage, args []Usage, textual [2]bool) (StringPlan, error) {
	p := StringPlan{name: name, result: result, text: text, count: uint8(len(args)), textual: textual}
	if !result.Valid() || !text.Valid() || text.Type.ID() != Text || len(args) < 1 || len(args) > 2 {
		return p, fmt.Errorf("invalid string signature")
	}
	switch name {
	case "||":
		if len(args) != 2 || result.Type.ID() != Text || !textual[0] && !textual[1] {
			return p, fmt.Errorf("invalid concatenation signature")
		}
	case "btrim", "ltrim", "rtrim", "normalize":
		if result.Type.ID() != Text || !textual[0] || len(args) == 2 && !textual[1] {
			return p, fmt.Errorf("invalid string function signature")
		}
	case "is_normalized":
		if result.Type.ID() != Bool || !textual[0] || len(args) == 2 && !textual[1] {
			return p, fmt.Errorf("invalid normalized predicate signature")
		}
	default:
		return p, fmt.Errorf("invalid string provider name")
	}
	for i, u := range args {
		if !u.Valid() {
			return p, fmt.Errorf("invalid string input usage")
		}
		p.args[i] = u
		if textual[i] {
			if !StringTextInput(u.Type.ID()) {
				return p, fmt.Errorf("invalid string text input")
			}
			if u.Type.ID() != Char {
				var err error
				p.conversions[i], err = PlanCoercion(u, text, Implicit)
				if err != nil {
					return p, err
				}
			}
		} else if !StringScalar(u.Type.ID()) {
			return p, fmt.Errorf("invalid mixed string input")
		}
	}
	return p, nil
}
func (p StringPlan) Evaluate(ctx context.Context, values [2]Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for i := range int(p.count) {
		if values[i].Type() != p.args[i].Type {
			return Value{}, fmt.Errorf("foreign string input value")
		}
		if values[i].IsNull() {
			return Null(p.result.Type), nil
		}
	}
	var texts [2]string
	for i := range int(p.count) {
		v := values[i]
		var err error
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
