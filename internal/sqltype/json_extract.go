package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// JSONExtractPlan owns the resolved signature, never invocation path state.
type JSONExtractPlan struct {
	name                   string
	result, document, path Usage
	variadic               bool
}

func BindJSONExtract(name string, result, document, path Usage, variadic bool) (JSONExtractPlan, error) {
	p := JSONExtractPlan{name, result, document, path, variadic}
	id := JSON
	if name == "json_extract_path_text" {
		id = Text
	} else if name != "json_extract_path" {
		return p, fmt.Errorf("invalid JSON extraction name")
	}
	pathID := Text
	if variadic {
		pathID = 1009
	}
	if !result.Valid() || !document.Valid() || !path.Valid() || result.Typmod != -1 || document.Typmod != -1 || path.Typmod != -1 || result.Type.ID() != id || document.Type.ID() != JSON || path.Type.ID() != pathID {
		return p, fmt.Errorf("invalid JSON extraction signature")
	}
	if id == JSON && result.Type != document.Type {
		return p, fmt.Errorf("foreign JSON extraction result")
	}
	if id == Text && !variadic && result.Type != path.Type {
		return p, fmt.Errorf("foreign JSON extraction text")
	}
	if variadic && (path.Type.element == nil || path.Type.element.ID() != Text || id == Text && result.Type != path.Type.element) {
		return p, fmt.Errorf("foreign JSON extraction path")
	}
	return p, nil
}
func (p JSONExtractPlan) Name() string { return p.name }
func (p JSONExtractPlan) Evaluate(ctx context.Context, values []Value) (Value, error) {
	if p.result.Type == nil || len(values) < 2 || len(values) > 100 || p.variadic && len(values) != 2 {
		return Value{}, fmt.Errorf("invalid JSON extraction values")
	}
	for i, v := range values {
		typ := p.path.Type
		if i == 0 {
			typ = p.document.Type
		}
		if v.typ != typ {
			return Value{}, fmt.Errorf("foreign JSON extraction argument")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, v := range values {
		if v.null {
			return Null(p.result.Type), nil
		}
	}
	path := values[1:]
	if p.variadic {
		if values[1].array == nil {
			return Value{}, fmt.Errorf("missing JSON extraction path payload")
		}
		path = values[1].array.elements
	}
	return extractJSON(ctx, values[0], path, p.result.Type, true, true)
}

// Each component has independently mutable released array-index state. Object
// keys borrow immutable path strings. State is local even for held statements.
type jsonExtractComponent struct {
	name           string
	index, current int
	ok             bool
}
type jsonExtractFrame struct {
	key             string
	active, matched bool
	start           int
	kind            byte
	decoded         string
}
type jsonExtraction struct {
	path                  []jsonExtractComponent
	frames                []jsonExtractFrame
	objects, arrays, text bool
	result                Value
	resultType            *Descriptor
}

func (x *jsonExtraction) keepDecoded(depth int, state jsonState) bool {
	if depth > len(x.path) {
		return false
	}
	if depth > 0 && (state == jsonObjectFirst || state == jsonObjectKey) {
		return x.objects && x.path[depth-1].ok
	}
	if !x.text {
		return false
	}
	if depth == 0 {
		return len(x.path) == 0
	}
	if depth != len(x.path) {
		return false
	}
	p := x.path[depth-1]
	if !p.ok {
		return false
	}
	if state == jsonObjectValue {
		return x.objects && x.frames[depth].key == p.name
	}
	return x.arrays && (state == jsonArrayFirst || state == jsonArrayValue) && p.current+1 == p.index
}
func jsonPathIndex(s string) int {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	if s == "" {
		return math.MinInt32
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return math.MinInt32
	}
	return int(n)
}
func extractJSON(ctx context.Context, doc Value, path []Value, result *Descriptor, objects, arrays bool) (Value, error) {
	var local [16]jsonExtractComponent
	parts := local[:0]
	if len(path) > len(local) {
		parts = make([]jsonExtractComponent, 0, len(path))
	}
	for _, v := range path {
		if v.null {
			return Null(result), nil
		}
		parts = append(parts, jsonExtractComponent{name: v.text, index: jsonPathIndex(v.text)})
	}
	return extractJSONComponents(ctx, doc, parts, result, objects, arrays)
}
func extractJSONComponents(ctx context.Context, doc Value, parts []jsonExtractComponent, result *Descriptor, objects, arrays bool) (Value, error) {
	var localFrames [17]jsonExtractFrame
	frames := localFrames[:min(len(parts)+1, len(localFrames))]
	if len(parts)+1 > len(localFrames) {
		frames = make([]jsonExtractFrame, len(parts)+1)
	}
	x := jsonExtraction{path: parts, frames: frames, objects: objects, arrays: arrays, text: result.ID() == Text, result: Null(result), resultType: result}
	if len(parts) > 0 {
		parts[0].ok = true
	}
	scan := jsonScanResult{extract: &x}
	if _, err := doc.typ.scanJSON(ctx, doc.text, &scan); err != nil {
		return Value{}, err
	}
	return x.result, nil
}
func (x *jsonExtraction) finish(l *jsonLexer, depth int, state jsonState) {
	if depth >= len(x.frames) || (state != jsonEnd && state != jsonObjectNext && state != jsonArrayNext) {
		return
	}
	f := &x.frames[depth]
	if !f.active {
		return
	}
	f.active = false
	if depth > 0 && depth < len(x.path) && f.matched {
		x.path[depth].ok = false
	}
	if depth != len(x.path) || !f.matched {
		return
	}
	if x.text && f.kind == 'z' {
		x.result = Null(x.resultType)
		return
	}
	text := l.text[f.start:l.end]
	if x.text && f.kind == 's' {
		text = f.decoded
	}
	x.result = Value{typ: x.resultType, text: text}
}
func (x *jsonExtraction) token(l *jsonLexer, depth int, state jsonState) error {
	if depth >= len(x.frames) {
		return nil
	}
	f := &x.frames[depth]
	if state == jsonObjectFirst || state == jsonObjectKey {
		if l.kind == 's' {
			f.key = l.decoded
		}
		return nil
	}
	value := state == jsonValue || state == jsonObjectValue || state == jsonArrayFirst && l.kind != ']' || state == jsonArrayValue
	if !value {
		return nil
	}
	matched := depth == 0 && len(x.path) == 0
	if depth > 0 && depth <= len(x.path) {
		p := &x.path[depth-1]
		if state == jsonObjectValue {
			matched = x.objects && p.ok && f.key == p.name
		} else {
			p.current++
			matched = x.arrays && p.ok && p.current == p.index
		}
		if matched && depth < len(x.path) {
			x.path[depth].ok = true
		}
	}
	*f = jsonExtractFrame{active: true, matched: matched, start: l.start, kind: l.kind, decoded: l.decoded}
	if depth == 0 && len(x.path) == 0 && l.kind != '{' && l.kind != '[' {
		f.start = 0
	}
	// PostgreSQL adjusts the index on every array at this depth, even when
	// that branch did not match. Preserve that released behavior privately.
	if l.kind == '[' && x.arrays && depth < len(x.path) {
		p := &x.path[depth]
		p.current = -1
		if p.index < 0 && p.index != math.MinInt32 {
			count, err := jsonExtractArrayCount(*l)
			if err != nil {
				return err
			}
			if -p.index <= count {
				p.index += count
			}
		}
	}
	return nil
}
func jsonExtractArrayCount(l jsonLexer) (int, error) {
	l.decode = false
	l.discard = true
	depth, count, expect := 0, 0, true
	for {
		if err := l.next(); err != nil {
			return 0, err
		}
		switch l.kind {
		case 0:
			return 0, l.expected("JSON value")
		case ']':
			if depth == 0 {
				return count, nil
			}
			depth--
		case '}':
			depth--
		case ',':
			if depth == 0 {
				expect = true
			}
		default:
			if depth == 0 && expect {
				count++
				expect = false
			}
			if l.kind == '[' || l.kind == '{' {
				depth++
			}
		}
	}
}
func (p BinaryPlan) jsonExtraction(ctx context.Context, a, b Value) (Value, error) {
	switch p.name {
	case "#>", "#>>":
		if b.array == nil {
			return Value{}, fmt.Errorf("missing JSON extraction path payload")
		}
		return extractJSON(ctx, a, b.array.elements, p.result.Type, true, true)
	}
	if b.typ.ID() == Text {
		return extractJSON(ctx, a, []Value{b}, p.result.Type, true, false)
	}
	parts := [1]jsonExtractComponent{{index: int(b.integer)}}
	return extractJSONComponents(ctx, a, parts[:], p.result.Type, false, true)
}
