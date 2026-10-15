package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// JSONObjectAggregatePlan captures only immutable key/value/result generations.
// Each group and execution owns its writer and current output environment.
type JSONObjectAggregatePlan struct {
	key, input, result Usage
	strict, unique     bool
}

func BindJSONObjectAggregate(key, input, result Usage, strict, unique bool) (JSONObjectAggregatePlan, error) {
	if !key.Valid() || !input.Valid() || !result.Valid() || result.Typmod != -1 || (result.Type.ID() != JSON && result.Type.ID() != JSONB) {
		return JSONObjectAggregatePlan{}, fmt.Errorf("invalid JSON object aggregate usage")
	}
	usages := [3]Usage{key, input, result}
	for i, u := range usages {
		for _, other := range usages[:i] {
			if u.Type.ID() != 2249 && u.Type.ID() == other.Type.ID() && u.Type != other.Type {
				return JSONObjectAggregatePlan{}, fmt.Errorf("mismatched JSON object aggregate owner")
			}
			base, prior := u.Type.BaseType(), other.Type.BaseType()
			if base.ID() != 2249 && base.ID() == prior.ID() && base != prior {
				return JSONObjectAggregatePlan{}, fmt.Errorf("mismatched JSON object aggregate base owner")
			}
		}
	}
	return JSONObjectAggregatePlan{key: key, input: input, result: result, strict: strict, unique: unique}, nil
}

type JSONObjectAggregateState struct {
	plan         JSONObjectAggregatePlan
	frames       [1]jsonbFrame
	writer       jsonCreateWriter
	seen, sealed bool
	count        int
	value        Value
	failure      error
	keys         map[string]struct{}
}

func (p JSONObjectAggregatePlan) Init(ctx context.Context, env OutputEnvironment) (*JSONObjectAggregateState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.key.Valid() || !p.input.Valid() || !p.result.Valid() {
		return nil, fmt.Errorf("invalid JSON object aggregate plan")
	}
	s := &JSONObjectAggregateState{plan: p, writer: jsonCreateWriter{ctx: ctx, env: env, result: p.result.Type, binary: p.result.Type.ID() == JSONB}}
	s.writer.tree.frames = s.frames[:0]
	s.writer.open(jsonbObject)
	s.writer.separator(" ")
	if s.writer.binary {
		s.writer.tree.frames[0].skipNulls = p.strict
		s.writer.tree.frames[0].uniqueKeys = p.unique
	}
	if p.unique && !s.writer.binary {
		s.keys = make(map[string]struct{})
	}
	return s, nil
}

func (p JSONObjectAggregatePlan) Advance(ctx context.Context, s *JSONObjectAggregateState, key, value Value) error {
	if s == nil || s.plan != p || s.sealed || key.typ != p.key.Type || value.typ != p.input.Type {
		return fmt.Errorf("invalid JSON object aggregate state or datum")
	}
	if s.failure != nil {
		return s.failure
	}
	if err := ctx.Err(); err != nil {
		s.failure = err
		return err
	}
	if key.null {
		if p.result.Type.ID() == JSONB {
			s.failure = failure("22023", "field name must not be null")
		} else {
			s.failure = failure("22004", "null value not allowed for object key")
		}
		return s.failure
	}
	s.seen = true
	if p.strict && value.null && !p.unique {
		return nil
	}
	s.writer.ctx = ctx
	text, err := s.writer.key(key)
	if err != nil {
		s.failure = err
		return err
	}
	if p.unique && !s.writer.binary {
		if _, exists := s.keys[text]; exists {
			var quoted strings.Builder
			if err := writeJSONBString(ctx, &quoted, text); err != nil {
				s.failure = err
			} else {
				s.failure = failure("22030", "duplicate JSON object key value: "+quoted.String())
			}
			return s.failure
		}
		s.keys[text] = struct{}{}
		if p.strict && value.null {
			return nil
		}
	}
	if s.count != 0 {
		s.writer.separator(", ")
	}
	if err = s.writer.member(text, " : "); err == nil {
		err = s.writer.value(value, false)
	}
	if err != nil {
		s.failure = err
		return err
	}
	s.count++
	s.failure = ctx.Err()
	return s.failure
}

func (p JSONObjectAggregatePlan) Finalize(ctx context.Context, s *JSONObjectAggregateState) (Value, error) {
	if s == nil || s.plan != p {
		return Value{}, fmt.Errorf("invalid JSON object aggregate state")
	}
	if s.failure != nil {
		return Value{}, s.failure
	}
	if err := ctx.Err(); err != nil {
		s.failure = err
		return Value{}, err
	}
	if s.sealed {
		return s.value, nil
	}
	if !s.seen {
		s.value = Null(p.result.Type)
	} else {
		s.writer.ctx = ctx
		s.writer.separator(" ")
		if err := s.writer.close(jsonbObject); err != nil {
			s.failure = err
			return Value{}, err
		}
		if s.writer.binary {
			payload := &jsonbPayload{root: s.writer.tree.root}
			text, err := jsonbOutput(ctx, &payload.root)
			if err != nil {
				s.failure = err
				return Value{}, err
			}
			s.value = Value{typ: p.result.Type, text: text, jsonb: payload}
		} else {
			s.value = Value{typ: p.result.Type, text: s.writer.text.String()}
		}
	}
	if err := ctx.Err(); err != nil {
		s.failure = err
		return Value{}, err
	}
	s.sealed = true
	return s.value, nil
}
