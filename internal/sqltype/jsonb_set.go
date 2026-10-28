package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/workadmission"
)

// JSONBSetPlan owns signature authority, never mutable JSON storage.
type JSONBSetPlan struct {
	name   string
	args   [5]Usage
	count  int
	result Usage
}

func BindJSONBSet(name string, result Usage, args ...Usage) (JSONBSetPlan, error) {
	p := JSONBSetPlan{name: name, result: result, count: len(args)}
	count := 4
	if name == "jsonb_set_lax" {
		count = 5
	} else if name != "jsonb_set" && name != "jsonb_insert" {
		return p, fmt.Errorf("invalid JSONB mutation function")
	}
	ids := [5]ID{JSONB, 1009, JSONB, Bool, Text}
	if len(args) != count || !result.Valid() || result.Typmod != -1 || result.Type.ID() != JSONB {
		return p, fmt.Errorf("invalid JSONB replacement signature")
	}
	for i, u := range args {
		if !u.Valid() || u.Typmod != -1 || u.Type.ID() != ids[i] {
			return p, fmt.Errorf("invalid JSONB replacement argument usage")
		}
		if ids[i] == JSONB && u.Type != result.Type {
			return p, fmt.Errorf("foreign JSONB replacement descriptor")
		}
		p.args[i] = u
	}
	if !args[1].Type.IsArray() || args[1].Type.Element().ID() != Text || count == 5 && args[1].Type.Element() != args[4].Type {
		return p, fmt.Errorf("foreign JSONB replacement path descriptor")
	}
	return p, nil
}

func (p JSONBSetPlan) Name() string { return p.name }

// Evaluate validates occurrence-owned values before applying function strictness.
// Its value-receiver frame is also the public cancellation fixture's admission seam.
func (p JSONBSetPlan) Evaluate(ctx context.Context, values []Value) (Value, error) {
	if p.result.Type == nil || len(values) != p.count {
		return Value{}, fmt.Errorf("invalid JSONB replacement values")
	}
	for i, v := range values {
		if v.typ != p.args[i].Type {
			return Value{}, fmt.Errorf("foreign JSONB replacement value")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if values[0].null || values[1].null || values[3].null || p.count == 4 && values[2].null {
		return Null(p.result.Type), nil
	}
	if values[0].jsonb == nil || values[1].array == nil {
		return Value{}, fmt.Errorf("missing JSONB replacement payload")
	}
	mode := jsonbReplace
	create := values[3].boolean
	if p.name == "jsonb_insert" {
		mode = jsonbInsertBefore
		if create {
			mode = jsonbInsertAfter
		}
		create = true
	}
	replacement := jsonbNode{kind: jsonbNull}
	if p.count == 5 {
		treatment := values[4]
		if treatment.null {
			return Value{}, jsonbSetTreatmentError()
		}
		if values[2].null {
			switch treatment.text {
			case "raise_exception":
				return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "JSON value must not be null", Detail: `Exception was raised because null_value_treatment is "raise_exception".`, Hint: "To avoid, either change the null_value_treatment argument or ensure that an SQL NULL is not passed."}
			case "return_target":
				return values[0], nil
			case "delete_key":
				mode = jsonbDelete
			case "use_json_null":
			default:
				return Value{}, jsonbSetTreatmentError()
			}
		}
	}
	if !values[2].null {
		if values[2].jsonb == nil {
			return Value{}, fmt.Errorf("missing JSONB replacement payload")
		}
		replacement = values[2].jsonb.root
	}
	root, changed, err := jsonbSetPath(ctx, values[0].jsonb.root, values[1], replacement, create, mode)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !changed {
		return values[0], nil
	}
	payload := &jsonbPayload{root: root}
	text, err := jsonbOutput(ctx, &payload.root)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.result.Type, text: text, jsonb: payload}, nil
}

func jsonbSetTreatmentError() error {
	return failure("22023", `null_value_treatment must be "delete_key", "return_target", "use_json_null", or "raise_exception"`)
}

type jsonbMutationMode uint8

const (
	jsonbReplace jsonbMutationMode = iota
	jsonbDelete
	jsonbInsertBefore
	jsonbInsertAfter
)

type jsonbSetAncestor struct {
	node  jsonbNode
	index int
}

func jsonbSetPath(ctx context.Context, root jsonbNode, path Value, replacement jsonbNode, create bool, mode jsonbMutationMode) (jsonbNode, bool, error) {
	if path.ArrayRank() > 1 {
		return jsonbNode{}, false, failure("2202E", "wrong number of array subscripts")
	}
	if root.kind != jsonbObject && root.kind != jsonbArray {
		message := "cannot set path in scalar"
		if mode == jsonbDelete {
			message = "cannot delete path in scalar"
		}
		return jsonbNode{}, false, failure("22023", message)
	}
	if path.ArrayLen() == 0 || len(root.items) == 0 && len(root.fields) == 0 && (mode == jsonbDelete || !create) {
		return root, false, ctx.Err()
	}
	var local [16]jsonbSetAncestor
	ancestors := local[:0]
	node := root
	// Only deletion owns this admission class; rebuilding its ancestors must
	// retain that identity even though their leaf update is a replacement.
	var workClass workadmission.Class
	if mode == jsonbDelete {
		workClass = workadmission.WorkJSONBPathDelete
	}
	for depth := range path.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return jsonbNode{}, false, err
		}
		if workClass != 0 {
			if err := workadmission.Admit(ctx, workClass); err != nil {
				return jsonbNode{}, false, err
			}
		}
		step := path.ArrayElement(depth)
		if step.null {
			return jsonbNode{}, false, failure("22004", fmt.Sprintf("path element at position %d is null", depth+1))
		}
		last := depth == path.ArrayLen()-1
		index, exists := 0, false
		switch node.kind {
		case jsonbObject:
			lo, hi := 0, len(node.fields)
			for lo < hi {
				mid := lo + (hi-lo)/2
				cmp, err := compareJSONBKeys(ctx, node.fields[mid].key, step.text)
				if err != nil {
					return jsonbNode{}, false, err
				}
				if cmp < 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			index = lo
			if index < len(node.fields) {
				cmp, err := compareJSONBKeys(ctx, node.fields[index].key, step.text)
				if err != nil {
					return jsonbNode{}, false, err
				}
				exists = cmp == 0
			}
		case jsonbArray:
			n, err := jsonbSetIndex(ctx, step.text, depth+1)
			if err != nil {
				return jsonbNode{}, false, err
			}
			if n < 0 {
				n += int64(len(node.items))
			}
			exists = n >= 0 && n < int64(len(node.items))
			if n < 0 {
				index = 0
			} else if n >= int64(len(node.items)) {
				index = len(node.items)
			} else {
				index = int(n)
			}
		default:
			return root, false, ctx.Err()
		}
		if !exists && (!last || mode == jsonbDelete || !create) {
			return root, false, ctx.Err()
		}
		if !last {
			ancestors = append(ancestors, jsonbSetAncestor{node, index})
			if node.kind == jsonbObject {
				node = node.fields[index].value
			} else {
				node = node.items[index]
			}
			continue
		}
		changed, err := jsonbSetLeaf(ctx, node, index, exists, step.text, replacement, mode, workClass)
		if err != nil {
			return jsonbNode{}, false, err
		}
		for i := len(ancestors) - 1; i >= 0; i-- {
			a := ancestors[i]
			changed, err = jsonbSetLeaf(ctx, a.node, a.index, true, "", changed, jsonbReplace, workClass)
			if err != nil {
				return jsonbNode{}, false, err
			}
		}
		return changed, true, ctx.Err()
	}
	return root, false, ctx.Err()
}

func jsonbSetLeaf(ctx context.Context, node jsonbNode, index int, exists bool, key string, replacement jsonbNode, mode jsonbMutationMode, workClass workadmission.Class) (jsonbNode, error) {
	if mode == jsonbInsertBefore || mode == jsonbInsertAfter {
		if node.kind == jsonbObject && exists {
			return jsonbNode{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "cannot replace existing key", Hint: "Try using the function jsonb_set to replace key value."}
		}
		if node.kind == jsonbArray && exists && mode == jsonbInsertAfter {
			index++
		}
		// Splice without consuming the selected original array element.
		exists = false
	}
	remove := mode == jsonbDelete
	length := len(node.items)
	if node.kind == jsonbObject {
		length = len(node.fields)
	}
	n := length
	if remove {
		n--
	} else if !exists {
		n++
	}
	oldEnd, newEnd := index, index
	if exists {
		oldEnd++
	}
	if !remove {
		newEnd++
	}
	if node.kind == jsonbArray {
		items := make([]jsonbNode, n)
		if err := jsonbSetCopy(ctx, items[:index], node.items[:index], workClass); err != nil {
			return jsonbNode{}, err
		}
		if !remove {
			items[index] = replacement
		}
		if err := jsonbSetCopy(ctx, items[newEnd:], node.items[oldEnd:], workClass); err != nil {
			return jsonbNode{}, err
		}
		node.items = items
	} else {
		fields := make([]jsonbEntry, n)
		if err := jsonbSetCopy(ctx, fields[:index], node.fields[:index], workClass); err != nil {
			return jsonbNode{}, err
		}
		if !remove {
			if exists {
				fields[index] = node.fields[index]
				fields[index].value = replacement
			} else {
				fields[index] = jsonbEntry{key: key, value: replacement}
			}
		}
		if err := jsonbSetCopy(ctx, fields[newEnd:], node.fields[oldEnd:], workClass); err != nil {
			return jsonbNode{}, err
		}
		node.fields = fields
	}
	return node, ctx.Err()
}

func jsonbSetCopy[T any](ctx context.Context, dst, src []T, workClass workadmission.Class) error {
	for start := 0; start < len(src); start += jsonbWorkChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		if workClass != 0 {
			if err := workadmission.Admit(ctx, workClass); err != nil {
				return err
			}
		}
		end := min(start+jsonbWorkChunk, len(src))
		copy(dst[start:end], src[start:end])
	}
	return ctx.Err()
}

func jsonbSetIndex(ctx context.Context, text string, ordinal int) (int64, error) {
	invalid := func() (int64, error) {
		return 0, failure("22P02", fmt.Sprintf("path element at position %d is not an integer: \"%s\"", ordinal, text))
	}
	i := 0
	for i < len(text) && space(text[i]) {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		i++
	}
	negative := false
	if i < len(text) && (text[i] == '-' || text[i] == '+') {
		negative = text[i] == '-'
		i++
	}
	start := i
	limit := int64(2147483647)
	if negative {
		limit++
	}
	n := int64(0)
	for i < len(text) {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		c := text[i]
		if c < '0' || c > '9' {
			return invalid()
		}
		digit := int64(c - '0')
		if n > (limit-digit)/10 {
			return invalid()
		}
		n = n*10 + digit
		i++
	}
	if i == start {
		return invalid()
	}
	if negative {
		n = -n
	}
	return n, ctx.Err()
}
