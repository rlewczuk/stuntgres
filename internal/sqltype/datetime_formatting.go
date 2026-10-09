package sqltype

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// DatetimeFormatPlan owns only the selected temporal overloads. Numeric
// formatting and ordinary temporal input codecs remain separate authorities.
type DatetimeFormatPlan struct {
	name     string
	result   Usage
	source   Usage
	constant []datetimeNode
	conflict bool
}
type datetimeKeyword struct {
	word, kind string
	digit      bool
	mode       uint8
}
type datetimeNode struct {
	key           *datetimeKeyword
	literal       string
	class         uint8
	fm, tm        bool
	ordinal       uint8
	variableWidth bool
}

func BindDatetimeFormat(ctx context.Context, name string, result, source Usage, template *string) (DatetimeFormatPlan, error) {
	p := DatetimeFormatPlan{name: name, result: result, source: source}
	if name != "to_char" && name != "to_date" && name != "to_timestamp" {
		return p, fmt.Errorf("invalid datetime operation")
	}
	if !result.Valid() || !source.Valid() {
		return p, fmt.Errorf("invalid datetime usages")
	}
	id := source.Type.ID()
	if name == "to_char" {
		if result.Type.ID() != Text || (id != Timestamp && id != Timestamptz && id != Interval) {
			return p, fmt.Errorf("invalid datetime formatting signature")
		}
	} else if id != Text || (name == "to_date" && result.Type.ID() != Date) || (name == "to_timestamp" && result.Type.ID() != Timestamptz) {
		return p, fmt.Errorf("invalid datetime parsing signature")
	}
	if template != nil {
		var err error
		p.constant, err = compileDatetime(ctx, *template)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}
func (p DatetimeFormatPlan) Name() string { return p.name }
func (p DatetimeFormatPlan) Evaluate(ctx context.Context, source, template Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if source.Type() != p.source.Type || template.Type() == nil || template.Type().ID() != Text {
		return Value{}, fmt.Errorf("invalid datetime executable operands")
	}
	if source.IsNull() || template.IsNull() {
		return Null(p.result.Type), nil
	}
	nodes := p.constant
	if nodes == nil {
		var err error
		nodes, err = compileDatetime(ctx, template.text)
		if err != nil {
			return Value{}, err
		}
	}
	if p.name == "to_char" {
		return datetimeOutput(ctx, p.result.Type, source, nodes, template.text, output, p.conflict)
	}
	return datetimeInput(ctx, p.result.Type, source.text, nodes, input, p.conflict)
}
func datetimeSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' || c == '\v'
}
func datetimeSeparator(c byte) bool {
	return c > 0x20 && c < 0x7f && !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z')
}
func compileDatetime(ctx context.Context, template string) ([]datetimeNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes := make([]datetimeNode, 0, len(template))
	for i := 0; i < len(template); {
		if len(nodes)%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		n := datetimeNode{}
		if strings.HasPrefix(template[i:], "FM") || strings.HasPrefix(template[i:], "fm") {
			n.fm = true
			i += 2
		} else if strings.HasPrefix(template[i:], "TM") || strings.HasPrefix(template[i:], "tm") {
			n.tm = true
			i += 2
		}
		if i == len(template) {
			break
		}
		for k := range datetimeKeywords {
			key := &datetimeKeywords[k]
			if key.word[0] == template[i] && strings.HasPrefix(template[i:], key.word) {
				n.key = key
				break
			}
		}
		if n.key != nil {
			i += len(n.key.word)
			if strings.HasPrefix(template[i:], "TH") {
				n.ordinal = 1
				i += 2
			} else if strings.HasPrefix(template[i:], "th") {
				n.ordinal = 2
				i += 2
			} else if strings.HasPrefix(template[i:], "SP") {
				i += 2
			}
			nodes = append(nodes, n)
			continue
		}
		if template[i] == '"' {
			i++
			for i < len(template) && template[i] != '"' {
				if len(nodes)%64 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				if template[i] == '\\' && i+1 < len(template) {
					i++
				}
				_, width := utf8.DecodeRuneInString(template[i:])
				nodes = append(nodes, datetimeNode{literal: template[i : i+width]})
				i += width
			}
			if i < len(template) {
				i++
			}
			continue
		}
		if template[i] == '\\' && i+1 < len(template) && template[i+1] == '"' {
			i++
		}
		_, width := utf8.DecodeRuneInString(template[i:])
		n.literal = template[i : i+width]
		if datetimeSpace(template[i]) {
			n.class = 1
		} else if datetimeSeparator(template[i]) {
			n.class = 2
		}
		nodes = append(nodes, n)
		i += width
	}
	for i, node := range nodes {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if node.key == nil {
			continue
		}
		variable := node.fm || node.ordinal != 0 || i+1 == len(nodes)
		if !variable {
			next := nodes[i+1]
			variable = next.key != nil && !next.key.digit || next.key == nil && (len(next.literal) != 1 || next.literal[0] < '0' || next.literal[0] > '9')
		}
		nodes[i].variableWidth = variable
	}
	return nodes, nil
}
func datetimeISOStart(year int) int64 {
	jan4 := civilDay(year, 1, 4)
	weekday := (jan4 + 5) % 7
	if weekday < 0 {
		weekday += 7
	}
	return jan4 - weekday
}
func datetimeISO(y, m, d int) (int, int, int) {
	day := civilDay(y, m, d)
	year := y
	if day < datetimeISOStart(year) {
		year--
	} else if day >= datetimeISOStart(year+1) {
		year++
	}
	start := datetimeISOStart(year)
	return year, int((day-start)/7) + 1, int(day-start) + 1
}

func (p DatetimeFormatPlan) WithCollationConflict(conflict bool) DatetimeFormatPlan {
	p.conflict = conflict
	return p
}
func (p DatetimeFormatPlan) Retag(result, source Usage) (DatetimeFormatPlan, error) {
	next, err := BindDatetimeFormat(context.Background(), p.name, result, source, nil)
	next.constant = p.constant
	next.conflict = p.conflict
	return next, err
}

// WithConstantTemplate retains a mask that became a literal during coercion
// preflight. Dynamic masks remain evaluation-owned, without a shared cache.
func (p DatetimeFormatPlan) WithConstantTemplate(ctx context.Context, template Value) (DatetimeFormatPlan, bool, error) {
	if p.constant != nil || template.null {
		return p, false, nil
	}
	if template.typ == nil || template.typ.id != Text {
		return p, false, fmt.Errorf("invalid datetime template descriptor")
	}
	nodes, err := compileDatetime(ctx, template.text)
	if err != nil {
		return p, false, err
	}
	p.constant = nodes
	return p, true, nil
}
