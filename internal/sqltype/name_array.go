package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

type nameArrayReader struct {
	ctx   context.Context
	input string
	at    int
}

type nameArrayBound struct{ lower, upper int32 }

func (p *nameArrayReader) malformed(detail string) error {
	err := failure("22P02", fmt.Sprintf("malformed array literal: \"%s\"", p.input)).(*sqlstate.Error)
	err.Detail = detail
	return err
}

func (p *nameArrayReader) whitespace() {
	for p.at < len(p.input) && space(p.input[p.at]) {
		p.at++
	}
}

func (p *nameArrayReader) boundInteger() (int32, error) {
	p.whitespace()
	start := p.at
	if p.at < len(p.input) && (p.input[p.at] == '-' || p.input[p.at] == '+') {
		p.at++
	}
	for p.at < len(p.input) && p.input[p.at] >= '0' && p.input[p.at] <= '9' {
		p.at++
	}
	n, err := strconv.ParseInt(p.input[start:p.at], 10, 32)
	if err != nil {
		return 0, p.malformed("Missing array dimension value.")
	}
	p.whitespace()
	return int32(n), nil
}

func (p *nameArrayReader) element() (string, bool, error) {
	p.whitespace()
	if p.at == len(p.input) {
		return "", false, p.malformed("Unexpected end of input.")
	}
	quoted := p.input[p.at] == '"'
	if quoted {
		p.at++
	}
	var out strings.Builder
	escaped := false
	end := 0
	for p.at < len(p.input) {
		if err := p.ctx.Err(); err != nil {
			return "", false, err
		}
		c := p.input[p.at]
		if c == '\\' {
			escaped = true
			p.at++
			if p.at == len(p.input) {
				return "", false, p.malformed("Unexpected end of input.")
			}
			out.WriteByte(p.input[p.at])
			p.at++
			end = out.Len()
			continue
		}
		if quoted && c == '"' {
			p.at++
			p.whitespace()
			if p.at == len(p.input) {
				return "", false, p.malformed("Unexpected end of input.")
			}
			if p.input[p.at] != ',' && p.input[p.at] != '}' {
				return "", false, p.malformed("Unexpected array element.")
			}
			return out.String(), false, nil
		}
		if !quoted && (c == ',' || c == '}') {
			text := out.String()[:end]
			if text == "" {
				return "", false, p.malformed(fmt.Sprintf("Unexpected \"%c\" character.", c))
			}
			return text, !escaped && strings.EqualFold(text, "NULL"), nil
		}
		if !quoted && (c == '{' || c == '"') {
			return "", false, p.malformed(fmt.Sprintf("Unexpected \"%c\" character.", c))
		}
		out.WriteByte(c)
		p.at++
		if quoted || !space(c) {
			end = out.Len()
		}
	}
	return "", false, p.malformed("Unexpected end of input.")
}

func writeNameArrayElement(out *strings.Builder, name string) {
	quoted := name == "" || strings.EqualFold(name, "NULL") || strings.ContainsAny(name, " ,{}\"\\\t\n\r\f\v")
	if quoted {
		out.WriteByte('"')
	}
	for _, c := range name {
		if c == '"' || c == '\\' {
			out.WriteByte('\\')
		}
		out.WriteRune(c)
	}
	if quoted {
		out.WriteByte('"')
	}
}

func (p *nameArrayReader) array(out *strings.Builder, depth int) ([]int, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 6 {
		return nil, failure("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", depth))
	}
	p.whitespace()
	if p.at == len(p.input) || p.input[p.at] != '{' {
		return nil, p.malformed("Array value must start with \"{\" or dimension information.")
	}
	p.at++
	p.whitespace()
	out.WriteByte('{')
	if p.at < len(p.input) && p.input[p.at] == '}' {
		p.at++
		out.WriteByte('}')
		return []int{0}, nil
	}
	count := 0
	var child []int
	nested := p.at < len(p.input) && p.input[p.at] == '{'
	for {
		if err := p.ctx.Err(); err != nil {
			return nil, err
		}
		p.whitespace()
		if p.at == len(p.input) {
			return nil, p.malformed("Unexpected end of input.")
		}
		if count != 0 {
			out.WriteByte(',')
		}
		if nested {
			if p.input[p.at] != '{' {
				return nil, p.malformed("Multidimensional arrays must have sub-arrays with matching dimensions.")
			}
			shape, err := p.array(out, depth+1)
			if err != nil {
				return nil, err
			}
			if count == 0 {
				child = shape
			} else {
				if len(child) != len(shape) {
					return nil, p.malformed("Multidimensional arrays must have sub-arrays with matching dimensions.")
				}
				for i, n := range child {
					if shape[i] != n {
						return nil, p.malformed("Multidimensional arrays must have sub-arrays with matching dimensions.")
					}
				}
			}
		} else {
			name, isNull, err := p.element()
			if err != nil {
				return nil, err
			}
			if isNull {
				out.WriteString("NULL")
			} else {
				writeNameArrayElement(out, clipName(name))
			}
		}
		count++
		p.whitespace()
		if p.at == len(p.input) {
			return nil, p.malformed("Unexpected end of input.")
		}
		c := p.input[p.at]
		p.at++
		if c == '}' {
			out.WriteByte('}')
			return append([]int{count}, child...), nil
		}
		if c != ',' {
			return nil, p.malformed(fmt.Sprintf("Unexpected \"%c\" character.", c))
		}
	}
}

func (d *Descriptor) nameArrayInput(ctx context.Context, text string) (Value, error) {
	p := nameArrayReader{ctx: ctx, input: text}
	p.whitespace()
	var bounds []nameArrayBound
	for p.at < len(text) && text[p.at] == '[' {
		p.at++
		lower, err := p.boundInteger()
		if err != nil {
			return Value{}, err
		}
		if p.at == len(text) || text[p.at] != ':' {
			return Value{}, p.malformed("Missing \" : \" in array dimensions.")
		}
		p.at++
		upper, err := p.boundInteger()
		if err != nil {
			return Value{}, err
		}
		if upper < lower {
			return Value{}, failure("2202E", "upper bound cannot be less than lower bound")
		}
		if p.at == len(text) || text[p.at] != ']' {
			return Value{}, p.malformed("Missing \" ] \" in array dimensions.")
		}
		p.at++
		p.whitespace()
		bounds = append(bounds, nameArrayBound{lower, upper})
	}
	if len(bounds) > 0 {
		if p.at == len(text) || text[p.at] != '=' {
			return Value{}, p.malformed("Missing \"=\" after array dimensions.")
		}
		p.at++
	}
	var out strings.Builder
	shape, err := p.array(&out, 1)
	if err != nil {
		return Value{}, err
	}
	p.whitespace()
	if p.at != len(text) {
		return Value{}, p.malformed("Junk after closing right brace.")
	}
	if len(bounds) > 0 {
		if len(bounds) != len(shape) {
			return Value{}, p.malformed("Specified array dimensions do not match array contents.")
		}
		custom := false
		for i, bound := range bounds {
			if int64(bound.upper)-int64(bound.lower)+1 != int64(shape[i]) {
				return Value{}, p.malformed("Specified array dimensions do not match array contents.")
			}
			custom = custom || bound.lower != 1
		}
		if custom {
			var header strings.Builder
			for _, bound := range bounds {
				fmt.Fprintf(&header, "[%d:%d]", bound.lower, bound.upper)
			}
			header.WriteByte('=')
			header.WriteString(out.String())
			return Value{typ: d, text: header.String()}, nil
		}
	}
	for _, extent := range shape {
		if extent == 0 {
			return Value{typ: d, text: "{}"}, nil
		}
	}
	return Value{typ: d, text: out.String()}, nil
}
