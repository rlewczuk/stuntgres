package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/sqlstate"
)

// JSONBRecordResultPlan separates a worker's actual record descriptor from the
// caller's anonymous output labels. Construct it after worker conversion so a
// conversion error is not replaced by a caller ABI error, including empty sets.
type JSONBRecordResultPlan struct {
	source, target *Descriptor
	fields         []CoercionPlan // Nil for the common payload-sharing label-only case.
}

func PlanJSONBRecordResult(source, target *Descriptor, env InputEnvironment) (JSONBRecordResultPlan, error) {
	if !source.Concrete() || !source.BaseType().IsComposite() || !target.Concrete() || target.ID() != 2249 || !target.IsComposite() {
		return JSONBRecordResultPlan{}, fmt.Errorf("invalid JSONB record result descriptors")
	}
	plan := JSONBRecordResultPlan{source: source, target: target}
	if source == target {
		return plan, nil
	}
	actual, expected := source.BaseType().CompositeLen(), target.CompositeLen()
	if actual != expected {
		word := "attributes"
		if actual == 1 {
			word = "attribute"
		}
		return JSONBRecordResultPlan{}, jsonbRecordTupleError(fmt.Sprintf("Returned row contains %d %s, but query expects %d.", actual, word, expected))
	}
	needsCarrier := false
	for i := range expected {
		from, to := source.BaseType().CompositeMember(i).Usage.Type, target.CompositeMember(i).Usage.Type
		if from == to {
			continue
		}
		if from.ID() == to.ID() {
			return JSONBRecordResultPlan{}, fmt.Errorf("foreign JSONB record field generation")
		}
		if !jsonbRecordBinaryField(from, to) {
			return JSONBRecordResultPlan{}, jsonbRecordTupleError(fmt.Sprintf("Returned type %s at ordinal position %d, but query expects %s.", jsonbRecordResultName(from, env), i+1, jsonbRecordResultName(to, env)))
		}
		needsCarrier = true
	}
	if needsCarrier {
		plan.fields = make([]CoercionPlan, expected)
		for i := range expected {
			from, to := source.BaseType().CompositeMember(i).Usage.Type, target.CompositeMember(i).Usage.Type
			if from == to {
				continue
			}
			// Proven binary facts authorize native carrier interpretation
			// (for example signed INT4 bits as OID), not general coercibility.
			conversion, err := PlanCoercion(from.Usage(), to.Usage(), Implicit)
			if err != nil {
				return JSONBRecordResultPlan{}, err
			}
			plan.fields[i] = conversion
		}
	}
	return plan, nil
}

func jsonbRecordBinaryField(source, target *Descriptor) bool {
	if source == target {
		return true
	}
	source = source.BaseType()
	if source == target {
		return true
	}
	if source.ID() == target.ID() || target.IsDomain() {
		return false
	}
	for _, fact := range castMetadata {
		if fact.Source == source.ID() && fact.Target == target.ID() && fact.Context == 'i' && fact.Method == 'b' {
			return true
		}
	}
	return false
}

func jsonbRecordResultName(d *Descriptor, env InputEnvironment) string {
	// Captured namespace lookup owns user names, not builtin format_type names.
	if !d.IsBuiltin() && env.TypeDiagnosticName != nil {
		if name := env.TypeDiagnosticName(d.ID()); name != "" {
			return name
		}
	}
	return d.DiagnosticName()
}

func jsonbRecordTupleError(detail string) error {
	err := failure("42804", "function return row and query-specified return row do not match").(*sqlstate.Error)
	err.Detail = detail
	return err
}

// Apply retains the converted datum. Only a changed field carrier allocates a
// new owned field buffer; label-only relabels share the immutable tuple payload.
// Caller typmods are metadata, never a second input/coercion sizing request.
func (p JSONBRecordResultPlan) Apply(ctx context.Context, value Value, input InputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.source == nil || p.target == nil || value.typ != p.source {
		return Value{}, fmt.Errorf("invalid JSONB record result generation")
	}
	if value.null {
		return Null(p.target), nil
	}
	if p.fields == nil {
		value.typ = p.target
		return value, nil
	}
	fields := make([]Value, len(p.fields))
	for i, conversion := range p.fields {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		fields[i] = value.CompositeField(i)
		if conversion.source.Type != nil {
			var err error
			fields[i], err = conversion.Apply(ctx, fields[i], input, OutputEnvironment{})
			if err != nil {
				return Value{}, err
			}
		}
	}
	return OwnedCompositeValue(p.target, fields)
}
