package sqltype

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// SQLJSONKind identifies SQL syntax, independently of the legacy function overloads.
type SQLJSONKind uint8

const (
	SQLJSONArray SQLJSONKind = iota + 1
	SQLJSONObject
	SQLJSONParse
	SQLJSONScalar
	SQLJSONSerialize
)

// SQLJSONPlan is immutable; writers and uniqueness sets are execution local.
type SQLJSONPlan struct {
	kind           SQLJSONKind
	result         Usage
	inputs         []Usage
	absent, unique bool
}

func BindSQLJSON(kind SQLJSONKind, result Usage, inputs []Usage, absent, unique bool) (SQLJSONPlan, error) {
	if kind < SQLJSONArray || kind > SQLJSONSerialize || !result.Valid() || result.Typmod != -1 || (result.Type.ID() != JSON && result.Type.ID() != JSONB) {
		return SQLJSONPlan{}, fmt.Errorf("invalid SQL/JSON plan")
	}
	if kind >= SQLJSONParse && result.Type.ID() != JSON {
		return SQLJSONPlan{}, fmt.Errorf("invalid SQL/JSON conversion result")
	}
	if kind == SQLJSONObject && len(inputs)%2 != 0 || kind >= SQLJSONParse && len(inputs) != 1 {
		return SQLJSONPlan{}, fmt.Errorf("invalid SQL/JSON arity")
	}
	for _, u := range inputs {
		if u.Type != nil && !u.Valid() {
			return SQLJSONPlan{}, fmt.Errorf("invalid SQL/JSON input usage")
		}
	}
	p := SQLJSONPlan{kind: kind, result: result, inputs: append([]Usage(nil), inputs...), absent: absent, unique: unique}
	return p, nil
}
func (p SQLJSONPlan) Apply(ctx context.Context, values []Value, env OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result.Type == nil || len(values) != len(p.inputs) {
		return Value{}, fmt.Errorf("invalid SQL/JSON execution")
	}
	for i, v := range values {
		if v.typ != p.inputs[i].Type {
			return Value{}, fmt.Errorf("invalid SQL/JSON input generation")
		}
	}
	if p.kind == SQLJSONParse || p.kind == SQLJSONSerialize {
		v := values[0]
		if v.null {
			return Null(p.result.Type), nil
		}
		if p.unique {
			scan := jsonScanResult{classify: true, unique: true}
			if _, err := p.result.Type.scanJSON(ctx, v.text, &scan); err != nil {
				return Value{}, err
			}
			if scan.duplicate {
				return Value{}, failure("22030", "duplicate JSON object key value")
			}
			return Value{typ: p.result.Type, text: v.text}, nil
		}
		if v.typ == p.result.Type {
			return v, nil
		}
		if id := v.typ.BaseType().ID(); id == JSON || id == JSONB {
			return Value{typ: p.result.Type, text: v.text}, nil
		}
		return p.result.Type.jsonInput(ctx, v.text)
	}
	var frames [32]jsonbFrame
	w := jsonCreateWriter{ctx: ctx, env: env, result: p.result.Type, binary: p.result.Type.ID() == JSONB}
	w.tree.frames = frames[:0]
	if p.kind == SQLJSONScalar {
		if values[0].null {
			return Null(p.result.Type), nil
		}
		if id := values[0].typ.BaseType().ID(); id == JSON || id == JSONB {
			return Value{typ: p.result.Type, text: values[0].text}, nil
		}
		if err := w.value(values[0], false); err != nil {
			return Value{}, err
		}
	} else {
		kind := jsonbArray
		if p.kind == SQLJSONObject {
			kind = jsonbObject
		}
		w.open(kind)
		count := 0
		var keys map[string]struct{}
		if p.unique {
			keys = make(map[string]struct{}, len(values)/2)
		}
		for i := 0; i < len(values); i++ {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			v := values[i]
			if p.kind == SQLJSONObject {
				keyValue := v
				i++
				v = values[i]
				omitted := p.absent && v.null
				if omitted && !p.unique && !w.binary {
					continue
				}
				if keyValue.null {
					if w.binary {
						return Value{}, failure("22023", fmt.Sprintf("argument %d: key must not be null", i))
					}
					return Value{}, failure("22004", "null value not allowed for object key")
				}
				key, err := w.key(keyValue)
				if err != nil {
					return Value{}, err
				}
				if p.unique {
					if _, ok := keys[key]; ok {
						message := "duplicate JSON object key value"
						if !w.binary {
							message += fmt.Sprintf(": %q", key)
						}
						return Value{}, failure("22030", message)
					}
					keys[key] = struct{}{}
				}
				if omitted {
					continue
				}
				if count != 0 {
					w.separator(", ")
				}
				if err := w.member(key, " : "); err != nil {
					return Value{}, err
				}
			} else {
				if p.absent && v.null {
					continue
				}
				if count != 0 {
					w.separator(", ")
				}
			}
			if err := w.value(v, false); err != nil {
				return Value{}, err
			}
			count++
		}
		if err := w.close(kind); err != nil {
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

// JSONPredicate validates the complete source before consulting kind or duplicates.
// Syntax and Unicode validation failures are false; cancellation and other failures escape.
func JSONPredicate(ctx context.Context, value Value, kind string, unique bool) (bool, error) {
	if value.typ == nil || value.null {
		return false, fmt.Errorf("invalid JSON predicate datum")
	}
	scan := jsonScanResult{classify: true, unique: unique}
	_, err := value.typ.scanJSON(ctx, value.text, &scan)
	if err != nil {
		var d *sqlstate.Error
		if errors.As(err, &d) && (d.Code == "22P02" || d.Code == "22P05") {
			return false, nil
		}
		return false, err
	}
	if scan.duplicate {
		return false, nil
	}
	switch kind {
	case "", "VALUE":
		return true, nil
	case "ARRAY":
		return scan.root == '[', nil
	case "OBJECT":
		return scan.root == '{', nil
	case "SCALAR":
		return scan.root != '[' && scan.root != '{', nil
	default:
		return false, fmt.Errorf("invalid JSON predicate kind")
	}
}
