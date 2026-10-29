package sqltype

import (
	"strconv"
	"strings"
)

// Object identity is local to an execution and to a source/generated base. Equal
// JSON trees at distinct occurrences must not collapse into the same identity.
type jsonpathObject struct {
	origin uint64
	node   *jsonbNode
}

func (e *jsonpathExecutor) objectID(node *jsonbNode, origin uint64) int64 {
	if e.identities == nil {
		e.identities = make(map[jsonpathObject]int64)
	}
	key := jsonpathObject{origin: origin, node: node}
	if id, ok := e.identities[key]; ok {
		return id
	}
	id := e.nextID
	e.nextID++
	e.identities[key] = id
	return id
}

func jsonpathInteger(n int64) jsonbNode {
	v := normalizeNumeric(Value{text: strconv.FormatInt(n, 10)})
	return jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: v.text, exponent: v.exponent}}
}

// These result nodes are immutable, just like nodes borrowed from input payloads.
var jsonpathMethodTypes = [...]jsonbNode{
	{kind: jsonbString, text: "null"},
	{kind: jsonbString, text: "string"},
	{kind: jsonbString, text: "number"},
	{kind: jsonbString, text: "boolean"},
	{kind: jsonbString, text: "array"},
	{kind: jsonbString, text: "object"},
}
var jsonpathMethodBooleans = [...]jsonbNode{
	{kind: jsonbBoolean},
	{kind: jsonbBoolean, boolean: true},
}
var jsonpathMethodBooleanStrings = [...]jsonbNode{
	{kind: jsonbString, text: "false"},
	{kind: jsonbString, text: "true"},
}

func (e *jsonpathExecutor) scalarMethod(n *jsonpathNode, v *jsonbNode) (*jsonbNode, error) {
	switch n.text {
	case "integer", "bigint", "decimal":
		return e.numericMethod(n, v)
	case "type":
		return &jsonpathMethodTypes[v.kind], nil
	case "size":
		size := 1
		if v.kind == jsonbArray {
			size = len(v.items)
		}
		result := jsonpathInteger(int64(size))
		return &result, nil
	case "boolean":
		if v.kind == jsonbBoolean {
			return v, nil
		}
		var result, valid bool
		switch v.kind {
		case jsonbString:
			result, valid = jsonpathParseBoolean(v.text)
		case jsonbNumber:
			result, valid = jsonpathNumericBoolean(v.number)
		default:
			return nil, jsonpathStructure("22036", "jsonpath item method .boolean() can only be applied to a boolean, string, or numeric value")
		}
		if !valid {
			return nil, e.invalidBoolean(v)
		}
		if result {
			return &jsonpathMethodBooleans[1], nil
		}
		return &jsonpathMethodBooleans[0], nil
	case "string":
		switch v.kind {
		case jsonbString:
			return v, nil
		case jsonbBoolean:
			if v.boolean {
				return &jsonpathMethodBooleanStrings[1], nil
			}
			return &jsonpathMethodBooleanStrings[0], nil
		case jsonbNumber:
			var b strings.Builder
			if err := appendNumericOutput(e.ctx, &b, v.number.value()); err != nil {
				return nil, err
			}
			return &jsonbNode{kind: jsonbString, text: b.String()}, nil
		default:
			return nil, jsonpathStructure("22036", "jsonpath item method .string() can only be applied to a boolean, string, numeric, or datetime value")
		}
	default:
		return nil, failure("0A000", "stuntgres: JSONPath scalar methods are unsupported")
	}
}

// JSONPath uses numeric_out -> int4in, not numeric-to-bool SQL casting. Scale
// bearing values (even 1.0) fail lexical int4 input. At most ten coefficient
// digits can survive the width/range check, so successful conversion is bounded.
func jsonpathNumericBoolean(n jsonbDecimal) (bool, bool) {
	if n.scale != 0 || n.exponent < 0 {
		return false, false
	}
	if n.digits == "" {
		return false, true
	}
	width := int64(len(n.digits)) + n.exponent
	if width > 10 {
		return false, false
	}
	var value int64
	for i := range width {
		value *= 10
		if i < int64(len(n.digits)) {
			value += int64(n.digits[i] - '0')
		}
	}
	limit := int64(2147483647)
	if n.negative {
		limit++
	}
	return value != 0, value <= limit
}

// Unlike SQL boolin, the JSONPath string conversion does not trim whitespace.
// Only ASCII case-insensitive unique prefixes and exact 0/1 are accepted.
func jsonpathParseBoolean(text string) (bool, bool) {
	if text == "1" {
		return true, true
	}
	if text == "0" {
		return false, true
	}
	if len(text) == 0 || len(text) > 5 {
		return false, false
	}
	matches, result := 0, false
	for i, word := range [...]string{"true", "false", "yes", "no", "on", "off"} {
		if len(text) > len(word) {
			continue
		}
		match := true
		for j := range len(text) {
			c := text[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != word[j] {
				match = false
				break
			}
		}
		if match {
			matches++
			result = i == 0 || i == 2 || i == 4
		}
	}
	return result, matches == 1
}

func (e *jsonpathExecutor) invalidBoolean(v *jsonbNode) error {
	var b strings.Builder
	b.WriteString(`argument "`)
	if v.kind == jsonbNumber {
		if err := appendNumericOutput(e.ctx, &b, v.number.value()); err != nil {
			return err
		}
	} else {
		for start := 0; start < len(v.text); start += jsonbWorkChunk {
			if err := e.ctx.Err(); err != nil {
				return err
			}
			b.WriteString(v.text[start:min(start+jsonbWorkChunk, len(v.text))])
		}
	}
	b.WriteString(`" of jsonpath item method .boolean() is invalid for type boolean`)
	return jsonpathStructure("22036", b.String())
}
