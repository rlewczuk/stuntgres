package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ValidateAggregateDefinition checks real SFUNC/type ownership, never extension
// helper aliases. Publication and native replacement share this validation.
func ValidateAggregateDefinition(v catalog.Read, d *catalog.Procedure) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid owned aggregate definition") }
	if d == nil || d.Aggregate == nil || d.Aggregate.Helpers != ([2]catalog.RoutineIdentity{}) {
		return invalid()
	}
	a := &d.Aggregate.Definition
	modeValid := func(mode string) bool { return mode == "r" || mode == "s" || mode == "w" }
	if d.RoutineKind != catalog.AggregateRoutine || d.SQL != nil || d.Builtin != nil || d.Implementation != nil || d.Foreign != nil || d.Extension != (ids.ObjectAddress{}) || d.Strict || a.Function != d.Address().ObjectID || a.Kind != "n" || a.DirectArgs != 0 || !modeValid(a.FinalModify) || !modeValid(a.MovingFinalModify) || a.SortOperator != 0 || !a.HasInitial && a.Initial != "" || !a.HasMovingInitial && a.MovingInitial != "" {
		return invalid()
	}
	for _, id := range a.Functions[2:5] {
		if id != 0 {
			return invalid()
		}
	}
	inputs, err := AggregateSignature(v, d.Signature)
	if err != nil {
		return err
	}
	result, ok := v.LookupType(sqltype.ID(d.Result))
	if !ok || !result.Concrete() {
		return invalid()
	}
	var edges []catalog.Cast
	if recipe := d.Aggregate.BinaryRecipe; recipe != nil {
		edges = recipe.Casts
		if len(edges) == 0 {
			return invalid()
		}
	}
	used := make([]bool, len(edges))
	conversion := func(source, target sqltype.Usage) bool {
		if _, err := catalog.AggregateConversion(d.Aggregate.BinaryRecipe, source, target); err != nil {
			return false
		}
		for i, edge := range edges {
			if edge.Source == source.BaseUsage().Type.ID() && edge.Target == target.Type.ID() {
				used[i] = true
			}
		}
		return true
	}
	check := func(oid ids.OID, arguments []sqltype.Usage, expected *sqltype.Descriptor) (*catalog.Procedure, error) {
		support, found := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if !found || support.RoutineKind != catalog.FunctionRoutine || support.Builtin != nil && support.Builtin.ReturnsSet || support.SQL == nil && (support.Builtin == nil || support.Builtin.Providers == 0) && support.Implementation == nil {
			return nil, invalid()
		}
		formals, output, e := AggregateSupportTypes(v, nil, support, d.Aggregate, arguments)
		if e != nil || output.Type != expected {
			return nil, invalid()
		}
		for i, actual := range arguments {
			if !conversion(actual, formals[i]) {
				return nil, invalid()
			}
		}
		return support, nil
	}
	for _, moving := range []bool{false, true} {
		stateID, forwardID, inverseID, finalID := a.TransitionType, a.Functions[0], ids.OID(0), a.Functions[1]
		hasSeed, extra := a.HasInitial, a.FinalExtra
		if moving {
			stateID, forwardID, inverseID, finalID = a.MovingType, a.Functions[5], a.Functions[6], a.Functions[7]
			hasSeed, extra = a.HasMovingInitial, a.MovingFinalExtra
			if stateID == 0 {
				if forwardID != 0 || inverseID != 0 || finalID != 0 || a.MovingSpace != 0 || hasSeed {
					return invalid()
				}
				continue
			}
		}
		state, found := v.LookupType(sqltype.ID(stateID))
		if !found || !state.Concrete() {
			return invalid()
		}
		arguments := make([]sqltype.Usage, len(inputs)+1)
		arguments[0] = state.Usage()
		copy(arguments[1:], inputs)
		forward, e := check(forwardID, arguments, state)
		if e != nil {
			return e
		}
		if forward.Strict && !hasSeed && (len(inputs) == 0 || !conversion(inputs[0], state.Usage())) {
			return invalid()
		}
		if moving {
			inverse, e := check(inverseID, arguments, state)
			if e != nil {
				return e
			}
			if inverse.Strict != forward.Strict {
				return invalid()
			}
		}
		if finalID == 0 {
			if state != result {
				return invalid()
			}
		} else {
			finalInputs := arguments[:1]
			if extra {
				finalInputs = arguments
			}
			final, e := check(finalID, finalInputs, result)
			if e != nil {
				return e
			}
			if final.Strict && extra {
				return invalid()
			}
		}
	}
	for i, edge := range edges {
		if edge.OID == 0 || edge.ObjectRevision == 0 || edge.Source == edge.Target || edge.Method != "b" || edge.Context != "i" || edge.Function != (ids.ObjectAddress{}) || edge.InputCast != (ids.ObjectAddress{}) || edge.ResultCast != (ids.ObjectAddress{}) || !used[i] {
			return invalid()
		}
		for _, old := range edges[:i] {
			if old.Pair() == edge.Pair() {
				return invalid()
			}
		}
	}
	return nil
}

// AggregateSignature decodes the canonical catalog signature through catalog
// descriptors; it is shared by catalog validation and symbolic native replay.
func AggregateSignature(v catalog.Read, signature catalog.ProcedureSignature) ([]sqltype.Usage, error) {
	return aggregateSignature(v, nil, signature, true)
}

func aggregateSignature(v catalog.Read, reader catalog.DefinitionReader, signature catalog.ProcedureSignature, concrete bool) ([]sqltype.Usage, error) {
	var inputs []sqltype.Usage
	if signature == "" {
		return inputs, nil
	}
	inputs = make([]sqltype.Usage, 0, strings.Count(string(signature), ",")+1)
	for part := range strings.SplitSeq(string(signature), ",") {
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, err
		}
		typ, ok := catalog.DefinitionTypeAt(v, reader, sqltype.ID(id))
		if !ok || concrete && !typ.Concrete() {
			return nil, fmt.Errorf("stuntgres: invalid aggregate signature type")
		}
		inputs = append(inputs, typ.Usage())
	}
	return inputs, nil
}

// AggregateSupportTypes specializes one actual selected support declaration.
// A nil retained owner selects against the current catalog for CREATE. A retained
// owner uses only its admitted recipe and fixed coercions, including when its
// recipe is nil. No executable body or conversion is built by the type probe.
func AggregateSupportTypes(v catalog.Read, reader catalog.DefinitionReader, support *catalog.Procedure, retained *catalog.OwnedAggregate, inputs []sqltype.Usage) ([]sqltype.Usage, sqltype.Usage, error) {
	invalid := func() ([]sqltype.Usage, sqltype.Usage, error) {
		return nil, sqltype.Usage{}, fmt.Errorf("stuntgres: invalid aggregate support specialization")
	}
	declared, err := aggregateSignature(v, reader, support.Signature, false)
	if err != nil || len(declared) != len(inputs) {
		return invalid()
	}
	resultType, ok := catalog.DefinitionTypeAt(v, reader, sqltype.ID(support.Result))
	if !ok {
		return invalid()
	}
	result := resultType.Usage()
	polymorphic := !resultType.Concrete()
	for _, formal := range declared {
		polymorphic = polymorphic || !formal.Type.Concrete()
	}
	if !polymorphic {
		result = aggregateSupportCollations(declared, result, inputs)
		return declared, result, nil
	}
	context := aggregateCallTypes{view: v, reader: reader, retained: retained}
	operands := make([]functions.Argument, len(inputs))
	for i, input := range inputs {
		operands[i].Usage = input
	}
	symbol := support.Name()
	if support.Builtin != nil {
		symbol = support.Builtin.Identity.Name
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: symbol}}}, Args: make([]parser.Expr, len(inputs))}
	targets := append([]sqltype.Usage(nil), declared...)
	if family, found := functions.Lookup(symbol); found && family.ProbeCall != nil {
		if !family.ProbeCall(context, raw, operands, declared, targets) {
			return invalid()
		}
		if !resultType.Concrete() {
			if family.ResultCall == nil {
				return invalid()
			}
			result, ok = family.ResultCall(context, raw, targets)
			if !ok {
				return invalid()
			}
		}
	} else if support.Builtin != nil && support.Builtin.Providers&functions.EnumRoutineProvider != 0 {
		selected, resolution := functions.ResolveEnum(symbol, true, operands)
		if resolution != functions.Resolved || selected.Descriptor == nil {
			return invalid()
		}
		for i := range targets {
			targets[i] = selected.Descriptor.Usage()
		}
		if !resultType.Concrete() {
			result, ok = selected.Result(context)
			if !ok {
				return invalid()
			}
		}
	} else {
		return invalid()
	}
	if result.Type == nil || !result.Type.Concrete() {
		return invalid()
	}
	for _, target := range targets {
		if target.Type == nil || !target.Type.Concrete() {
			return invalid()
		}
	}
	result = aggregateSupportCollations(targets, result, inputs)
	return targets, result, nil
}

// Aggregate support executes under one derived input collation. The caller
// supplies that collation on its collatable inputs; detached specialization
// must retain the same exact formal usages, not reset them to type defaults.
func aggregateSupportCollations(formals []sqltype.Usage, result sqltype.Usage, inputs []sqltype.Usage) sqltype.Usage {
	for i := range formals {
		if formals[i].Collation.ID != 0 && inputs[i].Collation.ID != 0 {
			formals[i].Collation = inputs[i].Collation
		}
	}
	if result.Collation.ID != 0 && inputs[0].Collation.ID != 0 {
		result.Collation = inputs[0].Collation
	}
	return result
}

type aggregateCallTypes struct {
	view     catalog.Read
	reader   catalog.DefinitionReader
	retained *catalog.OwnedAggregate
}

func (c aggregateCallTypes) Type(id sqltype.ID) *sqltype.Descriptor {
	typ, _ := catalog.DefinitionTypeAt(c.view, c.reader, id)
	return typ
}

func (c aggregateCallTypes) ArrayType(element *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	return catalog.DefinitionArrayAt(c.view, c.reader, element)
}

func (c aggregateCallTypes) CommonUsage(usages []sqltype.Usage) sqltype.Usage {
	result, _ := sqltype.SelectCommonUsage(len(usages), func(i int) sqltype.Usage { return usages[i] }, c.Type(sqltype.Text).Usage(), func(source *sqltype.Descriptor, target sqltype.ID) bool {
		typ := c.Type(target)
		return typ != nil && c.CanCoerce(source.Usage(), typ.Usage(), sqltype.Implicit)
	})
	return result
}

func (c aggregateCallTypes) CanCoerce(source, target sqltype.Usage, mode sqltype.CoercionContext) bool {
	if c.retained != nil {
		return catalog.AggregateCanCoerce(c.retained.BinaryRecipe, source, target, mode)
	}
	return catalog.CanCoerce(c.view, source, target, mode) == nil
}
