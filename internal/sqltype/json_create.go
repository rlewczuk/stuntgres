package sqltype

import (
	"context"
	"fmt"
	"math"
	"strings"

	"stuntgres/internal/sqlstate"
)

// JSONCreateKind is the closed set of SQL JSON conversion and builder functions.
type JSONCreateKind uint8

const (
	ToJSON JSONCreateKind = iota + 1
	ToJSONB
	ArrayToJSON
	RowToJSON
	JSONBuildArray
	JSONBBuildArray
	JSONBuildObject
	JSONBBuildObject
)

func (k JSONCreateKind) Name() string {
	switch k {
	case ToJSON:
		return "to_json"
	case ToJSONB:
		return "to_jsonb"
	case ArrayToJSON:
		return "array_to_json"
	case RowToJSON:
		return "row_to_json"
	case JSONBuildArray:
		return "json_build_array"
	case JSONBBuildArray:
		return "jsonb_build_array"
	case JSONBuildObject:
		return "json_build_object"
	case JSONBBuildObject:
		return "jsonb_build_object"
	}
	return ""
}
func (k JSONCreateKind) Builder() bool { return k >= JSONBuildArray && k <= JSONBBuildObject }
func (k JSONCreateKind) binary() bool {
	return k == ToJSONB || k == JSONBBuildArray || k == JSONBBuildObject
}
func (k JSONCreateKind) object() bool { return k == JSONBuildObject || k == JSONBBuildObject }

// JSONCreatePlan captures argument generations; remapping must bind a new plan.
type JSONCreatePlan struct {
	kind   JSONCreateKind
	result Usage
	inputs []Usage
}

func BindJSONCreate(kind JSONCreateKind, result Usage, inputs ...Usage) (JSONCreatePlan, error) {
	id := JSON
	if kind.binary() {
		id = JSONB
	}
	if kind.Name() == "" || !result.Valid() || result.Type.ID() != id || result.Typmod != -1 || len(inputs) > 100 {
		return JSONCreatePlan{}, fmt.Errorf("invalid JSON creation result or arity")
	}
	if !kind.Builder() {
		if len(inputs) < 1 || len(inputs) > 2 || (kind == ToJSON || kind == ToJSONB) && len(inputs) != 1 {
			return JSONCreatePlan{}, fmt.Errorf("invalid JSON conversion arity")
		}
		if inputs[0].Type == nil {
			return JSONCreatePlan{}, fmt.Errorf("unresolved JSON conversion input")
		}
		base := inputs[0].Type.BaseType()
		if kind == ArrayToJSON && !base.IsArray() || kind == RowToJSON && !base.IsComposite() {
			return JSONCreatePlan{}, fmt.Errorf("invalid JSON container input")
		}
		if len(inputs) == 2 && (inputs[1].Type == nil || inputs[1].Type.BaseType().ID() != Bool) {
			return JSONCreatePlan{}, fmt.Errorf("invalid JSON pretty flag")
		}
	}
	for i, u := range inputs {
		if u.Type == nil {
			continue
		} // Provisional ANY occurrences are rejected by binder.finish.
		if !u.Valid() {
			return JSONCreatePlan{}, fmt.Errorf("invalid JSON input usage")
		}
		if u.Type.ID() == result.Type.ID() && u.Type != result.Type {
			return JSONCreatePlan{}, fmt.Errorf("mismatched JSON result owner")
		}
		for j := range i {
			if inputs[j].Type != nil && u.Type.ID() != 2249 && inputs[j].Type.ID() == u.Type.ID() && inputs[j].Type != u.Type {
				return JSONCreatePlan{}, fmt.Errorf("mismatched JSON argument owner")
			}
		}
	}
	return JSONCreatePlan{kind: kind, result: result, inputs: append([]Usage(nil), inputs...)}, nil
}

func (p JSONCreatePlan) Apply(ctx context.Context, values []Value, variadic bool, env OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result.Type == nil || len(values) != len(p.inputs) {
		return Value{}, fmt.Errorf("invalid JSON creation plan")
	}
	for i, v := range values {
		if v.typ == nil || v.typ != p.inputs[i].Type {
			return Value{}, fmt.Errorf("invalid JSON creation value generation")
		}
	}
	var frames [32]jsonbFrame
	w := jsonCreateWriter{ctx: ctx, env: env, binary: p.kind.binary(), result: p.result.Type}
	w.tree.frames = frames[:0]
	if !p.kind.Builder() {
		for _, v := range values {
			if v.null {
				return Null(p.result.Type), nil
			}
		}
		pretty := len(values) == 2 && values[1].boolean
		if err := w.value(values[0], pretty); err != nil {
			return Value{}, err
		}
	} else {
		count := len(values)
		var array Value
		if variadic {
			if count != 1 || !values[0].typ.BaseType().IsArray() {
				return Value{}, fmt.Errorf("invalid JSON variadic plan")
			}
			array = values[0]
			if array.null {
				return Null(p.result.Type), nil
			}
			if array.array == nil {
				return Value{}, fmt.Errorf("missing JSON variadic payload")
			}
			count = array.ArrayLen()
		}
		at := func(i int) Value {
			if variadic {
				return array.ArrayElement(i)
			}
			return values[i]
		}
		if p.kind.object() && count%2 != 0 {
			d := failure("22023", "argument list must have even number of elements").(*sqlstate.Error)
			d.Hint = "The arguments of " + p.kind.Name() + "() must consist of alternating keys and values."
			return Value{}, d
		}
		container := jsonbArray
		if p.kind.object() {
			container = jsonbObject
		}
		w.open(container)
		for i := 0; i < count; i++ {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if i != 0 {
				w.separator(", ")
			}
			v := at(i)
			if p.kind.object() {
				if v.null {
					if p.kind.binary() {
						return Value{}, failure("22023", fmt.Sprintf("argument %d: key must not be null", i+1))
					}
					return Value{}, failure("22004", "null value not allowed for object key")
				}
				key, err := w.key(v)
				if err != nil {
					return Value{}, err
				}
				if err := w.member(key, " : "); err != nil {
					return Value{}, err
				}
				i++
				v = at(i)
			}
			if err := w.value(v, false); err != nil {
				return Value{}, err
			}
		}
		if err := w.close(container); err != nil {
			return Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if w.binary {
		payload := &jsonbPayload{root: w.tree.root}
		text, err := jsonbOutput(ctx, &payload.root)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: p.result.Type, text: text, jsonb: payload}, nil
	}
	return Value{typ: p.result.Type, text: w.text.String()}, nil
}

type jsonCreateWriter struct {
	ctx    context.Context
	env    OutputEnvironment
	result *Descriptor
	binary bool
	text   strings.Builder
	tree   jsonbBuilder
}

func (w *jsonCreateWriter) open(kind jsonbKind) {
	if w.binary {
		w.tree.open(kind)
		return
	}
	if kind == jsonbObject {
		w.text.WriteByte('{')
	} else {
		w.text.WriteByte('[')
	}
}
func (w *jsonCreateWriter) close(kind jsonbKind) error {
	if w.binary {
		return w.tree.close(w.ctx)
	}
	if kind == jsonbObject {
		w.text.WriteByte('}')
	} else {
		w.text.WriteByte(']')
	}
	return w.ctx.Err()
}
func (w *jsonCreateWriter) separator(sep string) {
	if !w.binary {
		w.text.WriteString(sep)
	}
}
func (w *jsonCreateWriter) member(key, colon string) error {
	if w.binary {
		w.tree.frames[len(w.tree.frames)-1].key = key
		return w.ctx.Err()
	}
	if err := writeJSONBString(w.ctx, &w.text, key); err != nil {
		return err
	}
	w.text.WriteString(colon)
	return nil
}
func (w *jsonCreateWriter) raw(text string) error {
	for i := 0; i < len(text); i += jsonbWorkChunk {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		w.text.WriteString(text[i:min(i+jsonbWorkChunk, len(text))])
	}
	return w.ctx.Err()
}
func (w *jsonCreateWriter) key(v Value) (string, error) {
	d := v.typ.BaseType()
	if d.IsArray() || d.IsComposite() || d.id == JSON || d.id == JSONB {
		return "", failure("22023", "key value must be scalar, not array, composite, or json")
	}
	return jsonCreateScalarOutput(w.ctx, v, w.env)
}
func jsonCreateScalarOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	switch v.typ.BaseType().id {
	case Bool:
		if v.boolean {
			return "true", nil
		}
		return "false", nil
	case Date, Timestamp, Timestamptz:
		return jsonCreateDateTime(v, env), ctx.Err()
	}
	return v.typ.OutputWithEnvironment(ctx, v, env)
}
func (w *jsonCreateWriter) scalar(v Value) error {
	if v.null {
		if w.binary {
			w.tree.add(jsonbNode{kind: jsonbNull})
			return nil
		}
		w.text.WriteString("null")
		return nil
	}
	d := v.typ.BaseType()
	switch d.id {
	case JSON:
		if !w.binary {
			return w.raw(v.text)
		}
		parsed, err := w.result.jsonInput(w.ctx, v.text)
		if err != nil {
			return err
		}
		w.tree.add(parsed.jsonb.root)
		return nil
	case JSONB:
		if v.jsonb == nil {
			return fmt.Errorf("missing embedded JSONB payload")
		}
		if w.binary {
			w.tree.add(v.jsonb.root)
			return nil
		}
		return w.raw(v.text)
	case Bool:
		if w.binary {
			w.tree.add(jsonbNode{kind: jsonbBoolean, boolean: v.boolean})
			return nil
		}
	case Int2, Int4, Int8:
		if w.binary {
			decimal := exactOperand(v)
			w.tree.add(jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: decimal.text, exponent: decimal.exponent, scale: decimal.scale, negative: decimal.negative}})
			return w.ctx.Err()
		}
	case Numeric:
		if w.binary && v.special == numericFinite {
			w.tree.add(jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: v.text, exponent: v.exponent, scale: v.scale, negative: v.negative}})
			return w.ctx.Err()
		}
	}
	text, err := jsonCreateScalarOutput(w.ctx, v, w.env)
	if err != nil {
		return err
	}
	number := d.id == Int2 || d.id == Int4 || d.id == Int8 || d.id == Numeric && v.special == numericFinite || (d.id == Float4 || d.id == Float8) && !math.IsNaN(v.floating) && !math.IsInf(v.floating, 0)
	if w.binary {
		node := jsonbNode{kind: jsonbString, text: text}
		if number {
			decimal := v
			if d.id != Numeric {
				decimal, err = parseNumeric(text)
				if err != nil {
					return err
				}
			}
			node = jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: decimal.text, exponent: decimal.exponent, scale: decimal.scale, negative: decimal.negative}}
		}
		w.tree.add(node)
		return w.ctx.Err()
	}
	if number || d.id == Bool {
		return w.raw(text)
	}
	return writeJSONBString(w.ctx, &w.text, text)
}

// One explicit stack handles nested domains/composites and array dimensions.
// Frames borrow immutable payloads and never allocate a slice of child Values.
type jsonCreateFrame struct {
	value                              Value
	dim, index, offset, length, stride int
	opened, pretty                     bool
	kind                               jsonbKind
}

func (w *jsonCreateWriter) value(v Value, pretty bool) error {
	var local [32]jsonCreateFrame
	stack := local[:1]
	stack[0] = jsonCreateFrame{value: v, pretty: pretty}
	for len(stack) != 0 {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		f := &stack[len(stack)-1]
		if f.value.typ == nil {
			return fmt.Errorf("missing nested JSON descriptor")
		}
		d := f.value.typ.BaseType()
		if f.value.null || !d.IsArray() && !d.IsComposite() {
			if err := w.scalar(f.value); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if !f.opened {
			f.opened = true
			if d.IsComposite() {
				if !f.value.validCompositePayload() {
					return fmt.Errorf("invalid JSON composite payload")
				}
				f.kind, f.length = jsonbObject, d.CompositeLen()
			} else {
				if f.value.array == nil {
					return fmt.Errorf("missing JSON array payload")
				}
				f.kind = jsonbArray
				if f.value.ArrayRank() > 0 {
					f.length = int(f.value.ArrayDimension(f.dim).Length)
				}
				f.stride = 1
				for i := f.dim + 1; i < f.value.ArrayRank(); i++ {
					f.stride *= int(f.value.ArrayDimension(i).Length)
				}
			}
			w.open(f.kind)
		}
		if f.index == f.length {
			if err := w.close(f.kind); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
			continue
		}
		i := f.index
		f.index++
		if i != 0 {
			sep := ","
			if f.pretty {
				sep = ",\n "
			}
			w.separator(sep)
		}
		var child jsonCreateFrame
		if f.kind == jsonbObject {
			if err := w.member(d.CompositeMember(i).Name, ":"); err != nil {
				return err
			}
			child.value = f.value.CompositeField(i)
		} else if f.dim+1 < f.value.ArrayRank() {
			child = jsonCreateFrame{value: f.value, dim: f.dim + 1, offset: f.offset + i*f.stride}
		} else {
			child.value = f.value.ArrayElement(f.offset + i)
		}
		stack = append(stack, child)
	}
	return w.ctx.Err()
}
