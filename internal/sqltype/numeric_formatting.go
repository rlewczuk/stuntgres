package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// NumericFormatPlan retains immutable template data. Processor counters and
// descriptor adjustments are execution-local, including for constant masks.
type NumericFormatPlan struct {
	name           string
	result, source Usage
	constant       *numericTemplate
	diagnostic     *sqlstate.Error
}
type numericNode struct{ key, literal string }
type numericTemplate struct {
	nodes                                                                 []numericNode
	pre, post, multi, zeroStart, zeroEnd, signPre                         int
	decimal, zero, fm, blank, roman, science, shift, plus, minus, bracket bool
	sign                                                                  int // 0 absent, 1 leading, 2 trailing
}

func BindNumericFormat(ctx context.Context, name string, result, source Usage, mask *string) (NumericFormatPlan, error) {
	p := NumericFormatPlan{name: name, result: result, source: source}
	if !result.Valid() || !source.Valid() {
		return p, fmt.Errorf("invalid numeric formatting usages")
	}
	id := source.Type.ID()
	if name == "to_char" {
		if result.Type.ID() != Text || (id != Int4 && id != Int8 && id != Numeric && id != Float4 && id != Float8) {
			return p, fmt.Errorf("invalid numeric formatting signature")
		}
	} else if name != "to_number" || id != Text || result.Type.ID() != Numeric {
		return p, fmt.Errorf("invalid numeric parsing signature")
	}
	if mask != nil {
		return p.withMask(ctx, *mask)
	}
	return p, nil
}
func (p NumericFormatPlan) withMask(ctx context.Context, mask string) (NumericFormatPlan, error) {
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if numericFormatEmpty(p.name, len(mask)) {
		p.constant = &numericTemplate{}
		return p, nil
	}
	t, err := compileNumericFormat(ctx, mask)
	if err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok {
			copy := *diagnostic
			p.diagnostic = &copy
			return p, nil
		}
		return p, err
	}
	p.constant = &t
	return p, nil
}
func (p NumericFormatPlan) Name() string { return p.name }
func (p NumericFormatPlan) Retag(result, source Usage) (NumericFormatPlan, error) {
	next, err := BindNumericFormat(context.Background(), p.name, result, source, nil)
	next.constant, next.diagnostic = p.constant, p.diagnostic
	return next, err
}
func (p NumericFormatPlan) WithConstantTemplate(ctx context.Context, mask Value) (NumericFormatPlan, bool, error) {
	if p.constant != nil || p.diagnostic != nil || mask.null {
		return p, false, nil
	}
	if mask.typ == nil || mask.typ.id != Text {
		return p, false, fmt.Errorf("invalid numeric template descriptor")
	}
	next, err := p.withMask(ctx, mask.text)
	return next, err == nil, err
}
func (p NumericFormatPlan) Evaluate(ctx context.Context, source, mask Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if source.typ != p.source.Type || mask.typ == nil || mask.typ.id != Text {
		return Value{}, fmt.Errorf("invalid numeric executable operands")
	}
	if source.null || mask.null {
		return Null(p.result.Type), nil
	}
	if numericFormatEmpty(p.name, len(mask.text)) {
		if p.name == "to_number" {
			return Null(p.result.Type), nil
		}
		return Value{typ: p.result.Type, text: ""}, nil
	}
	if p.diagnostic != nil {
		copy := *p.diagnostic
		return Value{}, &copy
	}
	var t numericTemplate
	if p.constant != nil {
		t = *p.constant
	} else {
		var err error
		t, err = compileNumericFormat(ctx, mask.text)
		if err != nil {
			return Value{}, err
		}
	}
	if p.name == "to_number" {
		return numericFormatInput(ctx, p.result.Type, source.text, t)
	}
	text, err := numericFormatOutput(ctx, source, t)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text}, nil
}

// REL_18_0 wrapper bounds precede template compilation and do not report a
// resource SQL error. A length helper lets units cover them without huge masks.
func numericFormatEmpty(name string, length int) bool {
	limit := int((int64(1)<<31)-1) / 8
	if name == "to_char" {
		limit = int(((int64(1) << 31) - 1 - 4) / 8)
	}
	return length <= 0 || length >= limit
}

var numericFormatKeywords = []string{"EEEE", "eeee", "FM", "fm", "MI", "mi", "PL", "pl", "PR", "pr", "RN", "rn", "SG", "sg", "SP", "sp", "TH", "th", ",", ".", "0", "9", "B", "b", "C", "c", "D", "d", "G", "g", "L", "l", "S", "s", "V", "v"}

func compileNumericFormat(ctx context.Context, mask string) (numericTemplate, error) {
	t := numericTemplate{nodes: make([]numericNode, 0, len(mask))}
	quoted := false
	for i := 0; i < len(mask); {
		if len(t.nodes)%64 == 0 {
			if err := ctx.Err(); err != nil {
				return t, err
			}
		}
		if mask[i] == '"' {
			quoted = !quoted
			i++
			continue
		}
		if mask[i] == '\\' && i+1 < len(mask) && (quoted || mask[i+1] == '"') {
			i++
			_, w := utf8.DecodeRuneInString(mask[i:])
			t.nodes = append(t.nodes, numericNode{literal: mask[i : i+w]})
			i += w
			continue
		}
		key := ""
		if !quoted {
			for _, candidate := range numericFormatKeywords {
				if strings.HasPrefix(mask[i:], candidate) {
					key = candidate
					break
				}
			}
		}
		if key == "" {
			_, w := utf8.DecodeRuneInString(mask[i:])
			t.nodes = append(t.nodes, numericNode{literal: mask[i : i+w]})
			i += w
			continue
		}
		t.nodes = append(t.nodes, numericNode{key: key})
		i += len(key)
		if err := t.prepare(key); err != nil {
			return t, err
		}
	}
	return t, nil
}
func numericFormatError(message, detail string) error {
	err := failure("42601", message).(*sqlstate.Error)
	err.Detail = detail
	return err
}
func (t *numericTemplate) prepare(key string) error {
	k := numericFormatKind(key)
	fail := func(s string) error { return numericFormatError(s, "") }
	if t.science && k != "EEEE" {
		return fail(`"EEEE" must be the last pattern used`)
	}
	switch k {
	case "9":
		if t.bracket {
			return fail(`"9" must be ahead of "PR"`)
		}
		if t.shift {
			t.multi++
		} else if t.decimal {
			t.post++
		} else {
			t.pre++
		}
	case "0":
		if t.bracket {
			return fail(`"0" must be ahead of "PR"`)
		}
		if !t.zero && !t.decimal {
			t.zero = true
			t.zeroStart = t.pre + 1
		}
		if t.decimal {
			t.post++
		} else {
			t.pre++
		}
		t.zeroEnd = t.pre + t.post
	case "B":
		if t.pre == 0 && t.post == 0 && !t.zero {
			t.blank = true
		}
	case ".", "D":
		if t.decimal {
			return fail("multiple decimal points")
		}
		if t.shift {
			return fail(`cannot use "V" and decimal point together`)
		}
		t.decimal = true
	case "FM":
		t.fm = true
	case "S":
		if t.sign != 0 {
			return fail(`cannot use "S" twice`)
		}
		if t.plus || t.minus || t.bracket {
			return fail(`cannot use "S" and "PL"/"MI"/"SG"/"PR" together`)
		}
		if !t.decimal {
			t.sign = 1
			t.signPre = t.pre
		} else {
			t.sign = 2
		}
	case "MI", "PL", "SG":
		if t.sign != 0 {
			return fail(`cannot use "S" and "` + k + `" together`)
		}
		t.minus = t.minus || k == "MI" || k == "SG"
		t.plus = t.plus || k == "PL" || k == "SG"
	case "PR":
		if t.sign != 0 || t.plus || t.minus {
			return fail(`cannot use "PR" and "S"/"PL"/"MI"/"SG" together`)
		}
		t.bracket = true
	case "RN":
		if t.roman {
			return fail(`cannot use "RN" twice`)
		}
		t.roman = true
	case "V":
		if t.decimal {
			return fail(`cannot use "V" and decimal point together`)
		}
		t.shift = true
	case "EEEE":
		if t.science {
			return fail(`cannot use "EEEE" twice`)
		}
		if t.blank || t.fm || t.sign != 0 || t.bracket || t.minus || t.plus || t.roman || t.shift {
			return numericFormatError(`"EEEE" is incompatible with other formats`, `"EEEE" may only be used together with digit and decimal point patterns.`)
		}
		t.science = true
	}
	if t.roman && (t.blank || t.zero || t.decimal || t.sign != 0 || t.bracket || t.minus || t.plus || t.shift || t.science) {
		return numericFormatError(`"RN" is incompatible with other formats`, `"RN" may only be used together with "FM".`)
	}
	return nil
}

// Keyword spellings are static; normalization must not allocate per node on
// every execution of an already-compiled mask.
func numericFormatKind(key string) string {
	switch key {
	case "eeee":
		return "EEEE"
	case "fm":
		return "FM"
	case "mi":
		return "MI"
	case "pl":
		return "PL"
	case "pr":
		return "PR"
	case "rn":
		return "RN"
	case "sg":
		return "SG"
	case "sp":
		return "SP"
	case "th":
		return "TH"
	case "b":
		return "B"
	case "c":
		return "C"
	case "d":
		return "D"
	case "g":
		return "G"
	case "l":
		return "L"
	case "s":
		return "S"
	case "v":
		return "V"
	}
	return key
}
