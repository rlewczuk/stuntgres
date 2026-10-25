package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeConstructors(t *testing.T) {
	ctx := context.Background()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	definition, _ := r.RangeDefinition()
	one, three := rangeTypeUnitInput(t, definition.Subtype.Type, "1"), rangeTypeUnitInput(t, definition.Subtype.Type, "3")
	a, err := sqltype.RangeFromValues(ctx, r, one, three, "[]")
	if err != nil {
		t.Fatal(err)
	}
	rangeTypeUnitOutput(t, a, "[1,3]")
	b := rangeTypeUnitInput(t, r, "[8,9)")
	for _, tc := range []struct {
		lower, upper sqltype.Value
		flags, want  string
	}{
		{one, three, "[)", "[1,3)"},
		{sqltype.Null(definition.Subtype.Type), three, "[]", "(,3]"},
		{one, sqltype.Null(definition.Subtype.Type), "[]", "[1,)"},
		{three, three, "[]", "[3,3]"},
		{three, three, "[)", "empty"},
	} {
		v, err := sqltype.RangeFromValues(ctx, r, tc.lower, tc.upper, tc.flags)
		if err != nil {
			t.Fatal(err)
		}
		rangeTypeUnitOutput(t, v, tc.want)
	}
	many, err := sqltype.MultirangeFromValues(ctx, m, []sqltype.Value{b, a})
	if err != nil {
		t.Fatal(err)
	}
	rangeTypeUnitOutput(t, many, "{[1,3],[8,9)}")
	array, _ := view.LookupArray(r)
	members, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: -2, Length: 2}}, []sqltype.Value{b, a})
	if err != nil {
		t.Fatal(err)
	}
	viaArray, err := sqltype.MultirangeFromArray(ctx, m, members)
	if err != nil || !sqltype.SameDatum(many, viaArray) {
		t.Fatal("variadic member normalization", err)
	}
	text, _ := view.LookupType(sqltype.Text)
	literal := func(v sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage(), Property: expr.Properties{Volatility: expr.Immutable}}, Value: v}
	}
	for i, form := range []catalog.RangeConstructorForm{catalog.RangeConstructorTwo, catalog.RangeConstructorThree, catalog.MultirangeConstructorZero, catalog.MultirangeConstructorOne, catalog.MultirangeConstructorMany} {
		typ := r
		if form >= catalog.MultirangeConstructorZero {
			typ = m
		}
		provider := &catalog.RangeConstructor{Type: typ, Form: form}
		signature, err := provider.Signature(view)
		if err != nil {
			t.Fatal(err)
		}
		owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}).Owner
		d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(920001 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: typ.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(typ.ID()), Owner: owner, Volatility: expr.Immutable, Strict: form >= catalog.MultirangeConstructorZero, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
		view.State().SetDescriptor(d.Address(), d)
		view.State().BindName(catalog.DescriptorNameKey(d), d.Address())
		if err := (stored.ProcedureProvider{}).Validate(view, d); err != nil {
			t.Fatal("actual generated declaration", form, err)
		}
		if catalog.RangeConstructorOwner(d).ObjectID != ids.OID(typ.ID()) || provider.Source() == "" {
			t.Fatal("missing type ownership")
		}
		var args []expr.Expr
		switch form {
		case catalog.RangeConstructorTwo:
			args = []expr.Expr{literal(one), literal(three)}
		case catalog.RangeConstructorThree:
			args = []expr.Expr{literal(one), literal(three), literal(sqltype.String(text, "[]"))}
		case catalog.MultirangeConstructorOne:
			args = []expr.Expr{literal(a)}
		case catalog.MultirangeConstructorMany:
			args = []expr.Expr{literal(b), literal(a)}
		}
		n := plan.RangeConstructorExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Immutable}, RoutineReference: d}, Procedure: d, Ref: view.Owner().Domain.Ref(view.Epoch(), d.Address()), Args: args}
		if err := n.ValidateStoredProvider(); err != nil {
			t.Fatal("captured constructor shape", form, err)
		}
		if form == catalog.MultirangeConstructorMany {
			if provider.Variadic() != ids.OID(r.ID()) || signature != "900003" {
				t.Fatal("variadic declaration lost actual associated array")
			}
			n.Args = []expr.Expr{literal(members)}
			n.Variadic = true
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal("explicit variadic shape", err)
			}
			parameter := func(typ *sqltype.Descriptor) expr.Expr {
				return plan.ParameterExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{Volatility: expr.Immutable, ParameterDependent: true}}, Number: 1}
			}
			n.Args = []expr.Expr{parameter(r), literal(a)}
			n.Variadic = false
			n.Property.ParameterDependent = true
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(r)}}}
			unfolded, changed, err := exec.FoldStrictParameterNull(evaluation, n)
			if err != nil || changed {
				t.Fatal("expanded NULL member folded as NULL array formal", changed, err)
			}
			_, err = unfolded.Eval(evaluation)
			var nullMember *sqlstate.Error
			if !errors.As(err, &nullMember) || nullMember.Code != "22004" || nullMember.Message != "multirange values cannot contain null members" {
				t.Fatal("expanded parameter NULL member lost constructor error", err)
			}
			n.Args = []expr.Expr{parameter(array)}
			n.Variadic = true
			evaluation.Parameters = []sqltype.Value{sqltype.Null(array)}
			folded, changed, err := exec.FoldStrictParameterNull(evaluation, n)
			if err != nil || !changed {
				t.Fatal("NULL declared variadic array did not retain strict folding", changed, err)
			}
			value, err := folded.Eval(evaluation)
			if err != nil || !value.IsNull() || value.Type() != m {
				t.Fatal("NULL declared array fold changed result", value, err)
			}
		}
		invalid := *d
		invalid.Signature = "25"
		if err := catalog.ValidateRangeConstructor(view, &invalid); err == nil {
			t.Fatal("wrong generated signature accepted")
		}
	}
	_, err = sqltype.RangeFromValues(ctx, r, three, one, "[]")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22000" {
		t.Fatal("reversed bound diagnostic", err)
	}
	_, err = sqltype.RangeFromValues(ctx, r, one, three, "bad")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Hint != `Valid values are "[]", "[)", "(]", and "()".` {
		t.Fatal("flags diagnostic", err)
	}
	_, err = sqltype.MultirangeFromValues(ctx, m, []sqltype.Value{sqltype.Null(r), a})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22004" {
		t.Fatal("NULL array member is not strict SQL NULL", err)
	}
	nullArray, err := sqltype.MultirangeFromArray(ctx, m, sqltype.Null(array))
	if err != nil || !nullArray.IsNull() || nullArray.Type() != m {
		t.Fatal("NULL variadic array result", err)
	}
	_, checked, checkedMany := rangeTypeUnitFamily(t, sqltype.Int4, true)
	checkedDefinition, _ := checked.RangeDefinition()
	low := rangeTypeUnitInput(t, checkedDefinition.Subtype.Type, "-2147483648")
	high := rangeTypeUnitInput(t, checkedDefinition.Subtype.Type, "2147483647")
	_, err = sqltype.MultirangeFromSource(ctx, checkedMany, 2, func(i int) (sqltype.Value, error) {
		if i == 0 {
			return sqltype.Null(checked), nil
		}
		return sqltype.RangeFromValues(ctx, checked, low, high, "[]")
	})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
		t.Fatal("child provider error must precede NULL-member error", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := sqltype.RangeFromValues(canceled, r, one, three, "[]"); !errors.Is(err, context.Canceled) {
		t.Fatal("constructor cancellation", err)
	}
}

func TestRangeTypeCoercions(t *testing.T) {
	_, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	_, foreignRange, foreignMulti := rangeTypeUnitFamily(t, sqltype.Int4, false)
	for i, typ := range []*sqltype.Descriptor{r, m} {
		foreign := []*sqltype.Descriptor{foreignRange, foreignMulti}[i]
		for _, mode := range []sqltype.CoercionContext{sqltype.Implicit, sqltype.Assignment, sqltype.Explicit} {
			if _, err := sqltype.PlanCoercion(typ.Usage(), typ.Usage(), mode); err != nil {
				t.Fatal("same-generation family coercion", typ.Name(), mode, err)
			}
			_, err := sqltype.PlanCoercion(typ.Usage(), foreign.Usage(), mode)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" {
				t.Fatal("foreign family generation admitted", typ.Name(), mode, err)
			}
		}
	}
}

func TestRangeTypeExactEndpoints(t *testing.T) {
	ctx := context.Background()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Float4, false)
	definition, _ := r.RangeDefinition()
	for _, word := range []uint32{0x7fc00001, 0x7f800001, 0x80000000, 0x3f800000} {
		leaf, err := sqltype.Float4FromBits(definition.Subtype.Type, word)
		if err != nil {
			t.Fatal(err)
		}
		value, err := sqltype.RangeFromValues(ctx, r, leaf, leaf, "[]")
		if err != nil {
			t.Fatal(err)
		}
		multi, err := sqltype.MultirangeFromValues(ctx, m, []sqltype.Value{value})
		if err != nil {
			t.Fatal(err)
		}
		next := catalog.CloneState(view.State())
		if _, err := typedef.ReownDomainTypes(next, nil, true, routine.RangeTypeRebinder(next, nil)); err != nil {
			t.Fatal(err)
		}
		for _, source := range []sqltype.Value{value, multi} {
			target := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Type().ID())}).SQLType
			mapped, err := source.MapRange(target, sqltype.RangeMapGeneration, func(v sqltype.Value, d *sqltype.Descriptor) (sqltype.Value, error) { return v.Retag(d) })
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []sqltype.Value{source, mapped} {
				count := 0
				if err := v.VisitRangeEndpoints(func(endpoint sqltype.Value) error {
					got, err := endpoint.Float4Bits()
					if err != nil || got != word {
						t.Fatalf("raw endpoint %#x want %#x: %v", got, word, err)
					}
					count++
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if count != 2 {
					t.Fatal("finite endpoints missing", count)
				}
			}
		}
	}
}
