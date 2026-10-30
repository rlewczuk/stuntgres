package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// JSONBPopulateRecord converts private immutable nodes, not their rendered whole
// document. The caller owns the concrete result shape and coherent input scope.
// A missing JSON document returns the original base, including whole SQL NULL.
func JSONBPopulateRecord(ctx context.Context, destination Usage, base, document Value, env InputEnvironment) (Value, error) {
	if err := util.ExecutionCheckpoint(ctx, "jsonb.populate_record"); err != nil {
		return Value{}, err
	}
	if !destination.Valid() || !destination.Type.BaseType().IsComposite() || base.typ != destination.Type || document.typ == nil || document.typ.ID() != JSONB {
		return Value{}, fmt.Errorf("invalid JSONB record invocation")
	}
	if document.null {
		return base, nil
	}
	if document.jsonb == nil {
		return Value{}, fmt.Errorf("invalid JSONB payload")
	}
	if document.jsonb.root.kind != jsonbObject {
		shape := "a scalar"
		if document.jsonb.root.kind == jsonbArray {
			shape = "an array"
		}
		return Value{}, failure("22023", "cannot call populate_composite on "+shape)
	}
	return populateJSONBField(ctx, destination, base, &document.jsonb.root, env, "")
}

// JSONBPopulateRecordset validates every row privately before making any row
// available. Each row uses the same original base; plans never retain this slice.
func JSONBPopulateRecordset(ctx context.Context, name string, destination Usage, base, document Value, env InputEnvironment) ([]Value, error) {
	if err := util.ExecutionCheckpoint(ctx, "jsonb.populate_record"); err != nil {
		return nil, err
	}
	if !destination.Valid() || !destination.Type.BaseType().IsComposite() || base.typ != destination.Type || document.typ == nil || document.typ.ID() != JSONB {
		return nil, fmt.Errorf("invalid JSONB recordset invocation")
	}
	if document.null {
		return nil, nil
	}
	if document.jsonb == nil {
		return nil, fmt.Errorf("invalid JSONB payload")
	}
	root := &document.jsonb.root
	if root.kind != jsonbArray {
		return nil, failure("22023", "cannot call "+name+" on a non-array")
	}
	rows := make([]Value, len(root.items))
	for i := range root.items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if root.items[i].kind != jsonbObject {
			return nil, failure("22023", "argument of "+name+" must be an array of objects")
		}
		v, err := populateJSONBField(ctx, destination, base, &root.items[i], env, "")
		if err != nil {
			return nil, err
		}
		rows[i] = v
	}
	return rows, ctx.Err()
}

func populateJSONBField(ctx context.Context, u Usage, base Value, node *jsonbNode, env InputEnvironment, key string) (Value, error) {
	if err := workadmission.Admit(ctx, workadmission.WorkJSONBRecordPopulate); err != nil {
		return Value{}, err
	}
	d := u.Type
	if d.IsDomain() {
		physical := u.BaseUsage()
		old, err := base.BaseValue()
		if err != nil {
			return Value{}, err
		}
		v, err := populateJSONBField(ctx, physical, old, node, env, key)
		if err != nil {
			return Value{}, err
		}
		if err := d.CheckDomainInput(ctx, v, env); err != nil {
			return Value{}, err
		}
		return d.Wrap(v)
	}
	if node == nil || node.kind == jsonbNull {
		return Null(d), nil
	}
	if d.id == JSONB {
		text, err := jsonbOutput(ctx, node)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: d, text: text, jsonb: &jsonbPayload{root: *node}}, nil
	}
	if d.id == JSON {
		text, err := jsonbOutput(ctx, node)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: d, text: text}, nil
	}
	if d.IsComposite() && node.kind != jsonbString {
		if node.kind != jsonbObject {
			shape := "a scalar"
			if node.kind == jsonbArray {
				shape = "an array"
			}
			return Value{}, failure("22023", "cannot call populate_composite on "+shape)
		}
		if len(node.fields) == 0 && !base.null {
			return base, nil
		}
		var fields []Value
		if base.null {
			fields = make([]Value, d.CompositeLen())
		}
		for i := range d.CompositeLen() {
			if err := workadmission.Admit(ctx, workadmission.WorkJSONBRecordPopulate); err != nil {
				return Value{}, err
			}
			member := d.CompositeMember(i)
			child, err := jsonbExtractField(ctx, node, member.Name)
			if err != nil {
				return Value{}, err
			}
			old := Null(member.Usage.Type)
			if !base.null {
				old = base.CompositeField(i)
				if child == nil {
					continue
				}
			}
			next, err := populateJSONBField(ctx, member.Usage, old, child, env, member.Name)
			if err != nil {
				return Value{}, err
			}
			if !base.null && fields == nil {
				if next == old {
					continue
				}
				fields = make([]Value, d.CompositeLen())
				copy(fields, base.composite.fields)
			}
			fields[i] = next
		}
		if !base.null && fields == nil {
			return base, nil
		}
		return OwnedCompositeValue(d, fields)
	}
	if d.IsArray() && node.kind != jsonbString {
		return populateJSONBArray(ctx, u, node, env, key)
	}
	text := node.text
	if node.kind != jsonbString {
		var err error
		text, err = jsonbOutput(ctx, node)
		if err != nil {
			return Value{}, err
		}
	}
	return d.InputWithEnvironment(ctx, text, u.Typmod, env)
}

func jsonbArrayShapeError(key string, indices []int, ragged bool) error {
	e := failure("22P02", "expected JSON array").(*sqlstate.Error)
	if ragged {
		e.Message = "malformed JSON array"
		e.Detail = "Multidimensional arrays must have sub-arrays with matching dimensions."
		return e
	}
	if len(indices) == 0 {
		if key != "" {
			e.Hint = "See the value of key \"" + key + "\"."
		}
		return e
	}
	var hint strings.Builder
	hint.WriteString("See the array element ")
	for _, index := range indices {
		hint.WriteByte('[')
		hint.WriteString(strconv.Itoa(index))
		hint.WriteByte(']')
	}
	if key != "" {
		hint.WriteString(" of key \"")
		hint.WriteString(key)
		hint.WriteByte('"')
	}
	hint.WriteByte('.')
	e.Hint = hint.String()
	return e
}

func populateJSONBArray(ctx context.Context, u Usage, node *jsonbNode, env InputEnvironment, key string) (Value, error) {
	if node.kind != jsonbArray {
		return Value{}, jsonbArrayShapeError(key, nil, false)
	}
	element := u.Type.Element()
	var local [6]ArrayDimension
	dimensions := local[:0]
	leaf := node
	count := int64(1)
	for {
		if err := workadmission.Admit(ctx, workadmission.WorkJSONBRecordPopulate); err != nil {
			return Value{}, err
		}
		if len(dimensions) == 6 {
			return Value{}, failure("54000", "number of array dimensions exceeds the maximum allowed (6)")
		}
		length := len(leaf.items)
		if int64(length) > math.MaxInt32 || length != 0 && count > math.MaxInt32/int64(length) {
			return Value{}, failure("54000", "array size exceeds the maximum allowed")
		}
		count *= int64(length)
		dimensions = append(dimensions, ArrayDimension{Lower: 1, Length: int32(length)})
		if length == 0 || leaf.items[0].kind != jsonbArray {
			break
		}
		leaf = &leaf.items[0]
	}
	elements := make([]Value, int(count))
	at := 0
	var indices [6]int
	var walk func(*jsonbNode, int) error
	walk = func(n *jsonbNode, depth int) error {
		if err := workadmission.Admit(ctx, workadmission.WorkJSONBRecordPopulate); err != nil {
			return err
		}
		if n.kind != jsonbArray {
			return jsonbArrayShapeError(key, indices[:depth], false)
		}
		for i := range n.items {
			child := &n.items[i]
			if depth+1 < len(dimensions) {
				indices[depth] = i
				if err := walk(child, depth+1); err != nil {
					return err
				}
				continue
			}
			usage := element.Usage()
			usage.Typmod = u.Typmod
			if element.IsDomain() {
				usage = element.Usage()
			}
			v, err := populateJSONBField(ctx, usage, Null(element), child, env, "")
			if err != nil {
				return err
			}
			// Conversion errors within a mismatched sub-array precede its shape
			// diagnostic. Do not allocate a larger buffer that cannot publish.
			if at < len(elements) {
				elements[at] = v
			}
			at++
		}
		if len(n.items) != int(dimensions[depth].Length) {
			return jsonbArrayShapeError(key, nil, true)
		}
		return nil
	}
	if err := walk(node, 0); err != nil {
		return Value{}, err
	}
	if count == 0 {
		return OwnedArrayValue(u.Type, nil, nil)
	}
	// Transfer an exact-sized dimensions buffer, not a stack-backed borrowed view.
	dims := make([]ArrayDimension, len(dimensions))
	copy(dims, dimensions)
	return OwnedArrayValue(u.Type, dims, elements)
}
