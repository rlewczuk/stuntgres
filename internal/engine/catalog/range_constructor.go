package catalog

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// RangeConstructor is a finite native declaration owned by one immutable type
// generation. It is neither a builtin OID nor an arbitrary function callback.
type RangeConstructor struct {
	Type *sqltype.Descriptor
	Form RangeConstructorForm
}
type RangeConstructorForm uint8

const (
	RangeConstructorTwo RangeConstructorForm = iota + 1
	RangeConstructorThree
	MultirangeConstructorZero
	MultirangeConstructorOne
	MultirangeConstructorMany
)

func (r RangeConstructor) Source() string {
	switch r.Form {
	case RangeConstructorTwo:
		return "range_constructor2"
	case RangeConstructorThree:
		return "range_constructor3"
	case MultirangeConstructorZero:
		return "multirange_constructor0"
	case MultirangeConstructorOne:
		return "multirange_constructor1"
	case MultirangeConstructorMany:
		return "multirange_constructor2"
	}
	return ""
}
func (r RangeConstructor) Variadic() ids.OID {
	if r.Form == MultirangeConstructorMany && r.Type != nil && r.Type.IsMultirange() {
		return ids.OID(r.Type.RangeType().ID())
	}
	return 0
}
func (r RangeConstructor) Signature(view Read) (ProcedureSignature, error) {
	if r.Type == nil {
		return "", fmt.Errorf("missing generated constructor type")
	}
	family := r.Type.RangeType()
	if family == nil {
		return "", fmt.Errorf("invalid generated constructor family")
	}
	if _, custom := family.RangeDefinition(); !custom {
		return "", fmt.Errorf("generated constructor requires a custom family")
	}
	id := func(t *sqltype.Descriptor) string { return strconv.FormatUint(uint64(t.ID()), 10) }
	switch r.Form {
	case RangeConstructorTwo, RangeConstructorThree:
		definition, ok := r.Type.RangeDefinition()
		if !ok {
			break
		}
		s := id(definition.Subtype.Type) + "," + id(definition.Subtype.Type)
		if r.Form == RangeConstructorThree {
			s += ",25"
		}
		return ProcedureSignature(s), nil
	case MultirangeConstructorZero:
		if r.Type.IsMultirange() {
			return "", nil
		}
	case MultirangeConstructorOne:
		if r.Type.IsMultirange() {
			return ProcedureSignature(id(r.Type.RangeType())), nil
		}
	case MultirangeConstructorMany:
		if r.Type.IsMultirange() {
			t, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.Type.RangeType().ID())})
			if ok && t.Array != 0 {
				return ProcedureSignature(strconv.FormatUint(uint64(t.Array), 10)), nil
			}
		}
	}
	return "", fmt.Errorf("invalid generated constructor form")
}
func ValidateRangeConstructor(view Read, d *Procedure) error {
	if d == nil || d.RangeConstructor == nil {
		return fmt.Errorf("missing generated constructor provider")
	}
	r := d.RangeConstructor
	t, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Result)})
	if !ok || t.SQLType != r.Type || d.SQL != nil || d.Builtin != nil || d.Implementation != nil || d.Aggregate != nil || d.Foreign != nil || d.RoutineKind != FunctionRoutine || d.Volatility != expr.Immutable || d.Strict != (r.Form >= MultirangeConstructorZero) {
		return fmt.Errorf("invalid generated constructor capture")
	}
	if d.SQLBody != nil || d.SQLDefaults != nil || len(d.Body) != 0 || d.Conversion.Name() != "" || d.AccessMethod != NoAccessMethod || d.Extension != (ids.ObjectAddress{}) || d.Dictionary != (ids.ObjectAddress{}) {
		return fmt.Errorf("conflicting generated constructor provider")
	}
	signature, err := r.Signature(view)
	if err != nil {
		return err
	}
	owner := t
	if r.Type.IsMultirange() {
		owner, ok = view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.Type.RangeType().ID())})
		if !ok {
			return fmt.Errorf("missing range constructor namespace owner")
		}
	}
	if d.Signature != signature || d.Name() != r.Type.Name() || d.Namespace() != owner.Namespace() || r.Source() == "" {
		return fmt.Errorf("invalid generated constructor identity")
	}
	return nil
}
func RangeConstructorOwner(d *Procedure) ids.ObjectAddress {
	if d == nil || d.RangeConstructor == nil || d.RangeConstructor.Type == nil {
		return ids.ObjectAddress{}
	}
	return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RangeConstructor.Type.ID())}
}
