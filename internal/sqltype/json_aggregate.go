package sqltype

import (
	"context"
	"fmt"
)

// JSONArrayAggregatePlan owns immutable input/result generations. Mutable
// writers and output environments belong to an execution-local state only.
type JSONArrayAggregatePlan struct {
	input, result Usage
	absent        bool
}

func BindJSONArrayAggregate(input, result Usage, absent bool) (JSONArrayAggregatePlan, error) {
	if !input.Valid() || !result.Valid() || result.Typmod != -1 || (result.Type.ID() != JSON && result.Type.ID() != JSONB) {
		return JSONArrayAggregatePlan{}, fmt.Errorf("invalid JSON aggregate input or result usage")
	}
	if input.Type.ID() == result.Type.ID() && input.Type != result.Type {
		return JSONArrayAggregatePlan{}, fmt.Errorf("mismatched JSON aggregate result owner")
	}
	return JSONArrayAggregatePlan{input: input, result: result, absent: absent}, nil
}

type JSONArrayAggregateState struct {
	plan         JSONArrayAggregatePlan
	frames       [1]jsonbFrame
	writer       jsonCreateWriter
	seen, sealed bool
	count        int
	value        Value
	failure      error
}

func (p JSONArrayAggregatePlan) Init(ctx context.Context, env OutputEnvironment) (*JSONArrayAggregateState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.input.Valid() || !p.result.Valid() {
		return nil, fmt.Errorf("invalid JSON aggregate plan")
	}
	s := &JSONArrayAggregateState{plan: p, writer: jsonCreateWriter{ctx: ctx, env: env, result: p.result.Type, binary: p.result.Type.ID() == JSONB}}
	s.writer.tree.frames = s.frames[:0]
	s.writer.open(jsonbArray)
	return s, nil
}

func (p JSONArrayAggregatePlan) Advance(ctx context.Context, s *JSONArrayAggregateState, v Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.plan != p || s.sealed || v.typ != p.input.Type {
		return fmt.Errorf("invalid JSON aggregate state or datum")
	}
	if s.failure != nil {
		return s.failure
	}
	s.seen = true
	if p.absent && v.null {
		return nil
	}
	s.writer.ctx = ctx
	if s.count != 0 {
		s.writer.separator(", ")
		base := v.typ.BaseType()
		if !v.null && (base.IsArray() || base.IsComposite()) {
			s.writer.separator("\n ")
		}
	}
	if err := s.writer.value(v, false); err != nil {
		s.failure = err
		return err
	}
	s.count++
	s.failure = ctx.Err()
	return s.failure
}

func (p JSONArrayAggregatePlan) Finalize(ctx context.Context, s *JSONArrayAggregateState) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if s == nil || s.plan != p {
		return Value{}, fmt.Errorf("invalid JSON aggregate state")
	}
	if s.failure != nil {
		return Value{}, s.failure
	}
	if s.sealed {
		return s.value, nil
	}
	if !s.seen {
		s.value = Null(p.result.Type)
	} else {
		s.writer.ctx = ctx
		if err := s.writer.close(jsonbArray); err != nil {
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

// SQL/JSON conversion plans stay distinct from ordinary cast admission. They
// reuse the same immutable coercion representation and assignment sizing.
func PlanJSONFormattedInput(source, target Usage, encoding string) (CoercionPlan, error) {
	from, to := source.BaseUsage(), target.BaseUsage()
	if !source.Valid() || !target.Valid() || to.Type.ID() != JSON ||
		(from.Type.ID() != Bytea && !IsCharacter(from.Type.ID()) && from.Type.ID() != Name && from.Type.ID() != Char) {
		return CoercionPlan{}, fmt.Errorf("invalid formatted JSON input plan")
	}
	p := CoercionPlan{source: from, target: to, method: jsonFormattedInput, context: Explicit, jsonEncoding: encoding}
	if source.Type.IsDomain() || target.Type.IsDomain() {
		p.logicalSource, p.logicalTarget = source.Type, target.Type
	}
	return p, nil
}

func PlanJSONUniqueInput(source, target Usage, encoding string) (CoercionPlan, error) {
	p, err := PlanJSONFormattedInput(source, target, encoding)
	p.jsonUnique = true
	return p, err
}

func PlanJSONReturning(source, target Usage) (CoercionPlan, error) {
	if !source.Valid() || !target.Valid() {
		return CoercionPlan{}, fmt.Errorf("invalid JSON returning usage")
	}
	from, to := source.BaseUsage(), target.BaseUsage()
	if from.Type.ID() != JSON && from.Type.ID() != JSONB {
		return CoercionPlan{}, fmt.Errorf("invalid JSON returning source")
	}
	if to.Type.ID() == JSON || to.Type.ID() == JSONB {
		p, err := PlanCoercion(source, target, Assignment)
		p.jsonReturning = true
		return p, err
	}
	if !IsCharacter(to.Type.ID()) && to.Type.ID() != Name && to.Type.ID() != Char && to.Type.ID() != Bytea {
		return CoercionPlan{}, failure("42846", fmt.Sprintf("cannot cast type %s to %s", source.Type.DiagnosticName(), target.Type.DiagnosticName()))
	}
	p := CoercionPlan{source: from, target: to, method: jsonReturningOutput, context: Assignment, jsonReturning: true}
	if source.Type.IsDomain() || target.Type.IsDomain() {
		p.logicalSource, p.logicalTarget = source.Type, target.Type
	}
	return p, nil
}

// Rebind preserves admitted binary and SQL/JSON conversion operations across
// descriptor generations without consulting a later catalog edge.
func (p CoercionPlan) Rebind(source, target Usage, mode CoercionContext) (CoercionPlan, error) {
	if p.binary.method != binaryCoercionNone {
		next, err := PlanDeclaredBinaryCoercion(source, target)
		if err == nil && next.binary.method != p.binary.method {
			err = fmt.Errorf("binary coercion representation changed")
		}
		return next, err
	}
	if p.jsonReturning {
		return PlanJSONReturning(source, target)
	}
	switch p.method {
	case jsonFormattedInput:
		if p.jsonUnique {
			return PlanJSONUniqueInput(source, target, p.jsonEncoding)
		}
		return PlanJSONFormattedInput(source, target, p.jsonEncoding)
	default:
		next, err := PlanCoercion(source, target, mode)
		if err != nil {
			return CoercionPlan{}, err
		}
		before, after := &p, &next
		for before != nil && after != nil {
			if before.method != after.method {
				return CoercionPlan{}, fmt.Errorf("coercion operation changed")
			}
			before, after = before.element, after.element
		}
		if before != nil || after != nil {
			return CoercionPlan{}, fmt.Errorf("coercion element operation changed")
		}
		return next, nil
	}
}

func (p CoercionPlan) IsJSONFormattedInput() bool { return p.method == jsonFormattedInput }

// IsJSONReturning reports constructor-owned output conversion provenance.
func (p CoercionPlan) IsJSONReturning() bool { return p.jsonReturning }
