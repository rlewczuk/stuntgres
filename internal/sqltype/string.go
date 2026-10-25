package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

// StringTextInput identifies the string family's direct implicit TEXT inputs.
func StringTextInput(d *Descriptor) bool {
	if d == nil {
		return false
	}
	d = d.BaseType()
	return d.IsCitext() || IsCharacter(d.ID()) || d.ID() == Name || d.ID() == Char
}
func StringCategory(id ID) bool { return IsCharacter(id) || id == Name }

func StringScalar(d *Descriptor) bool { return d.Concrete() && !d.BaseType().IsArray() }

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
	if d.id == Regtype {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if env.Regtype == nil {
			return "", fmt.Errorf("missing regtype output snapshot")
		}
		return env.Regtype.FormatRegtype(v.oid)
	}
	if d.id == Regrole {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if env.Regrole == nil {
			return "", fmt.Errorf("missing regrole output snapshot")
		}
		return env.Regrole.FormatRegrole(v.oid)
	}
	if IsRegObjectAlias(d.id) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if d.id == Regcollation && env.Regcollation == nil || d.id == Regnamespace && env.Regnamespace == nil {
			return "", fmt.Errorf("missing collation/namespace output snapshot")
		}
		return d.regObjectOutput(v, env)
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
 citext bool
}

func (p StringPlan) Name() string                  { return p.name }
func (p StringPlan) Result() Usage                 { return p.result }
func (p StringPlan) TextUsage() Usage              { return p.text }
func (p StringPlan) ArgumentUsage(index int) Usage { return p.formal[index] }
func (p StringPlan) CollationIndeterminate() bool  { return p.conflict }
func (p StringPlan) Strict() bool                  { return p.name != "string_to_array" && p.name != "string_to_table" }

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
	case "parse_ident":
		if len(args) > 2 || len(args) == 1 && !textual[0] || len(args) == 2 && textual[0] == textual[1] || textual[2] || textual[3] {
			return p, fmt.Errorf("invalid parse_ident roles")
		}
		if result.Typmod != -1 || !result.Type.IsArray() || result.Type.Element() != text.Type || result.Type.ID() != text.Type.AssociatedArray() {
			return p, fmt.Errorf("foreign parse_ident result owner")
		}
	case "split_part":
		if len(args) != 3 || result.Type.ID() != Text || !textual[0] || !textual[1] || textual[2] {
			return p, fmt.Errorf("invalid split_part signature")
		}
	case "string_to_array", "string_to_table":
		if (len(args) != 2 && len(args) != 3) || !textual[0] || !textual[1] || len(args) == 3 && !textual[2] {
			return p, fmt.Errorf("invalid string split signature")
		}
		if name == "string_to_array" && (!result.Type.IsArray() || result.Type.Element() != text.Type || result.Type.ID() != text.Type.AssociatedArray()) || name == "string_to_table" && result.Type != text.Type {
			return p, fmt.Errorf("foreign string split result owner")
		}
	case "to_bin", "to_oct", "to_hex":
		if len(args) != 1 || result.Type.ID() != Text || result.Typmod != -1 || textual[0] {
			return p, fmt.Errorf("invalid base conversion signature")
		}
	case "md5":
		if len(args) != 1 || result.Type.ID() != Text || result.Typmod != -1 || !textual[0] {
			return p, fmt.Errorf("invalid text digest signature")
		}
	case "ascii":
		if len(args) != 1 || result != result.Type.Usage() || result.Type.ID() != Int4 || !textual[0] {
			return p, fmt.Errorf("invalid ascii signature")
		}
	case "chr":
		if len(args) != 1 || result.Type.ID() != Text || textual[0] {
			return p, fmt.Errorf("invalid chr signature")
		}
	case "unistr":
		if len(args) != 1 || result.Type.ID() != Text || !textual[0] {
			return p, fmt.Errorf("invalid unistr signature")
		}
	case "unicode_assigned":
		if len(args) != 1 || result != result.Type.Usage() || result.Type.ID() != Bool || !textual[0] {
			return p, fmt.Errorf("invalid assignment signature")
		}
	case "to_ascii":
		if len(args) > 2 || result.Type.ID() != Text || !textual[0] || len(args) == 2 && textual[1] {
			return p, fmt.Errorf("invalid to_ascii signature")
		}
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
	case "left", "right", "substring", "substr":
		if (len(args) != 2 && !(len(args) == 3 && (name == "substring" || name == "substr"))) || result.Type.ID() != Text || result.Typmod != -1 || !textual[0] || textual[1] || textual[2] || textual[3] {
			return p, fmt.Errorf("invalid text slice signature")
		}
	case "lpad", "rpad":
		if (len(args) != 2 && len(args) != 3) || result.Type.ID() != Text || !textual[0] || textual[1] || len(args) == 3 && !textual[2] {
			return p, fmt.Errorf("invalid string padding signature")
		}
	case "repeat":
		if len(args) != 2 || result.Type.ID() != Text || !textual[0] || textual[1] {
			return p, fmt.Errorf("invalid string repeat signature")
		}
	case "reverse":
		if len(args) != 1 || result.Type.ID() != Text || !textual[0] {
			return p, fmt.Errorf("invalid string reverse signature")
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
		if name == "parse_ident" && !textual[i] {
			if u.Type.ID() != Bool || u != u.Type.Usage() {
				return p, fmt.Errorf("invalid parse_ident boolean input")
			}
			p.formal[i] = u
			continue
		}
		if name == "to_ascii" && i == 1 {
			if (u.Type.ID() != Name && u.Type.ID() != Int4) || u != u.Type.Usage() {
				return p, fmt.Errorf("invalid encoding input")
			}
			p.formal[i] = u
			continue
		}
		if StringIntegerArgument(name, i) {
			if (u.Type.ID() != Int4 && !(StringBaseConversion(name) && u.Type.ID() == Int8)) || u != u.Type.Usage() {
				return p, fmt.Errorf("invalid string integer input")
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
			if !StringTextInput(u.Type) {
				return p, fmt.Errorf("invalid string text input")
			}
			if u.Type.ID() != Char {
				var err error
				p.conversions[i], err = PlanCoercion(u, target, Implicit)
				if err != nil {
					return p, err
				}
			}
		} else if !StringScalar(u.Type) {
			return p, fmt.Errorf("invalid mixed string input")
		}
	}
	return p, nil
}
func (p StringPlan) Evaluate(ctx context.Context, values [4]Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
 if p.citext {return Value{},fmt.Errorf("citext string evaluation requires regex execution")}
	if p.name == "string_to_table" {
		return Value{}, fmt.Errorf("string table plan requires a field cursor")
	}
	for i := range int(p.count) {
		if values[i].Type() != p.args[i].Type {
			return Value{}, fmt.Errorf("foreign string input value")
		}
	}
	if p.Strict() {
		for i := range int(p.count) {
			if values[i].IsNull() {
				return Null(p.result.Type), nil
			}
		}
	}
	texts, err := p.stringInputs(ctx, values, input, output)
	if err != nil {
		return Value{}, err
	}
	if p.name == "parse_ident" {
		source, strict := 0, true
		for i := range int(p.count) {
			if p.textual[i] {
				source = i
			} else {
				strict = values[i].boolean
			}
		}
		return parseIdent(ctx, p.result.Type, texts[source], strict)
	}
	if p.name == "split_part" {
		index, err := values[2].Integer()
		if err != nil {
			return Value{}, err
		}
		if p.conflict && index != 0 && texts[0] != "" && texts[1] != "" {
			return Value{}, stringComparisonCollationError()
		}
		field, err := splitPart(ctx, texts[0], texts[1], index)
		return String(p.result.Type, field), err
	}
	if p.name == "string_to_array" {
		if values[0].IsNull() {
			return Null(p.result.Type), nil
		}
		return splitArray(ctx, p, texts, values)
	}
	if StringBaseConversion(p.name) {
		return p.evaluateBaseConversion(values[0])
	}
	if p.name == "md5" {
		return binaryDigest(ctx, "md5", p.result.Type, texts[0])
	}
	if p.name == "ascii" || p.name == "chr" || p.name == "to_ascii" || p.name == "unistr" || p.name == "unicode_assigned" {
		return p.evaluateEncoding(ctx, texts, values)
	}
	if p.result.Type.ID() == Int4 {
		return p.evaluateLength(ctx, texts)
	}
	if p.name == "left" || p.name == "right" || p.name == "substring" || p.name == "substr" {
		return p.evaluateSlice(ctx, texts, values)
	}
	if p.name == "lpad" || p.name == "rpad" || p.name == "repeat" || p.name == "reverse" {
		return p.evaluatePadding(ctx, texts, values)
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
		mode := p.result.Collation.CaseMode()
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
