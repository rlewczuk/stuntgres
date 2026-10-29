package sqltype

import (
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Input-only headers are immutable and never retained in a JSON result.
var jsonpathIntegerDescriptors = [...]Descriptor{{id: Int4}, {id: Int8}}

// Numeric methods borrow immutable decimal fields and return descriptor-neutral
// JSON numbers. SQL codecs remain the authority for input, rounding and typmods.
func (e *jsonpathExecutor) numericMethod(n *jsonpathNode, input *jsonbNode) (*jsonbNode, error) {
	if err := e.ctx.Err(); err != nil {
		return nil, err
	}
	switch n.text {
	case "abs", "floor", "ceiling":
		if input.kind != jsonbNumber {
			return nil, jsonpathStructure("22036", "jsonpath item method ."+n.text+"() can only be applied to a numeric value")
		}
		v := input.number.value()
		if n.text == "abs" {
			v.negative = false
		} else {
			var err error
			v, err = numericCeilFloor(e.ctx, v, n.text == "ceiling")
			if err != nil {
				return nil, err
			}
		}
		return jsonpathNumericResult(v).jsonbNode, e.ctx.Err()
	}
	if input.kind != jsonbNumber && input.kind != jsonbString {
		return nil, jsonpathStructure("22036", "jsonpath item method ."+n.text+"() can only be applied to a string or numeric value")
	}
	var v Value
	var err error
	if n.text == "integer" || n.text == "bigint" {
		d := &jsonpathIntegerDescriptors[0]
		if n.text == "bigint" {
			d = &jsonpathIntegerDescriptors[1]
		}
		var integer int64
		if input.kind == jsonbString {
			v, err = d.integerInput(input.text)
			integer = v.integer
		} else {
			integer, err = numericInteger(input.number.value(), d.id)
		}
		if err != nil {
			return nil, e.invalidNumericMethod(n.text, input)
		}
		v = Value{text: strconv.FormatInt(integer, 10), negative: integer < 0}
		if v.negative {
			v.text = v.text[1:]
		}
		return jsonpathNumericResult(normalizeNumeric(v)).jsonbNode, e.ctx.Err()
	}
	if n.text == "double" {
		source := input.text
		if input.kind == jsonbNumber {
			var b strings.Builder
			if err := appendNumericOutput(e.ctx, &b, input.number.value()); err != nil {
				return nil, err
			}
			source = b.String()
		}
		v, err = jsonpathDoubleInput(e.ctx, source)
		if err != nil {
			return nil, e.invalidNumericMethod(n.text, input)
		}
		if math.IsNaN(v.floating) || math.IsInf(v.floating, 0) {
			return nil, jsonpathStructure("22036", "NaN or Infinity is not allowed for jsonpath item method .double()")
		}
		if input.kind == jsonbNumber {
			return input, e.ctx.Err()
		}
		v, err = numericCoercion(e.ctx, v, jsonpathNumericDescriptor.Usage())
		if err != nil {
			return nil, err
		}
		return jsonpathNumericResult(v).jsonbNode, e.ctx.Err()
	}
	if input.kind == jsonbString {
		v, err = jsonpathNumericDescriptor.numericInput(input.text, -1)
		if err != nil {
			return nil, e.invalidNumericMethod(n.text, input)
		}
	} else {
		v = input.number.value()
	}
	if err := e.ctx.Err(); err != nil {
		return nil, err
	}
	if v.special != numericFinite {
		return nil, jsonpathStructure("22036", "NaN or Infinity is not allowed for jsonpath item method ."+n.text+"()")
	}
	if n.text == "number" {
		if input.kind == jsonbNumber {
			return input, nil
		}
		return jsonpathNumericResult(v).jsonbNode, nil
	}
	if n.left != nil {
		var modifiers [2]int32
		count := 1
		for i, arg := range [...]*jsonpathNode{n.left, n.right} {
			if arg == nil {
				break
			}
			integer, err := numericInteger(arg.number, Int4)
			if err != nil {
				label := "precision"
				if i == 1 {
					label = "scale"
				}
				return nil, jsonpathStructure("22036", label+" of jsonpath item method .decimal() is out of range for type integer")
			}
			modifiers[i] = int32(integer)
			count = i + 1
		}
		u, _, err := jsonpathNumericDescriptor.ResolveModifiers(modifiers[:count]...)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok {
				return nil, jsonpathStructure(diagnostic.Code, diagnostic.Message)
			}
			return nil, err
		}
		v, err = sizeNumeric(v, u.Typmod)
		if err != nil {
			return nil, e.invalidNumericMethod(n.text, input)
		}
	}
	return jsonpathNumericResult(v).jsonbNode, e.ctx.Err()
}

func (e *jsonpathExecutor) invalidNumericMethod(method string, input *jsonbNode) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`argument "`)
	if input.kind == jsonbNumber {
		if err := appendNumericOutput(e.ctx, &b, input.number.value()); err != nil {
			return err
		}
	} else {
		for start := 0; start < len(input.text); start += jsonbWorkChunk {
			if err := e.ctx.Err(); err != nil {
				return err
			}
			b.WriteString(input.text[start:min(start+jsonbWorkChunk, len(input.text))])
		}
	}
	b.WriteString(`" of jsonpath item method .`)
	b.WriteString(method)
	b.WriteString(`() is invalid for type `)
	switch method {
	case "decimal", "number":
		b.WriteString("numeric")
	case "double":
		b.WriteString("double precision")
	default:
		b.WriteString(method)
	}
	return jsonpathStructure("22036", b.String())
}
