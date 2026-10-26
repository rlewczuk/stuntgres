package proc

import (
	"fmt"

	"stuntgres/internal/sqltype"
)

// ResolveResult uses the caller's family solver for every declared output. The
// tuple owner retains output labels, ordinals and unchanged descriptor identity.
func (d *Definition) ResolveResult(resolve func(sqltype.Usage) sqltype.Usage) (sqltype.Usage, error) {
	if !d.DerivedResult() {
		return resolve(d.Result), nil
	}
	if !d.MatchesOutputTuple(d.Result.Type) {
		return sqltype.Usage{}, fmt.Errorf("invalid routine output tuple")
	}
	var members []sqltype.CompositeMember
	for i := range d.Result.Type.CompositeLen() {
		member := d.Result.Type.CompositeMember(i)
		usage := resolve(member.Usage)
		if usage.Type == nil {
			return sqltype.Usage{}, nil
		}
		if !usage.Valid() {
			return sqltype.Usage{}, fmt.Errorf("indeterminate routine output type")
		}
		if members == nil && usage != member.Usage {
			members = make([]sqltype.CompositeMember, d.Result.Type.CompositeLen())
			for j := range i {
				members[j] = d.Result.Type.CompositeMember(j)
			}
		}
		if members != nil {
			member.Usage = usage
			members[i] = member
		}
	}
	if members == nil {
		return d.Result, nil
	}
	tuple, err := sqltype.RecordShape(members)
	if err != nil {
		return sqltype.Usage{}, err
	}
	return tuple.Usage(), nil
}

// ValidFor proves one coherent specialization of the current declaration.
// Descriptor pointers retain generation and record-shape identity, not just OIDs.
func (s InvocationSignature) ValidFor(d *Definition) bool {
	if d == nil || len(s.Inputs) != len(d.Inputs) {
		return false
	}
	var leaf [3]*sqltype.Descriptor
	var array [3]*sqltype.Descriptor
	var ranged [3]*sqltype.Descriptor
	check := func(formal, actual sqltype.Usage, outputMember bool) bool {
		if formal.Type == nil || actual.Type == nil || !actual.Valid() && !(d.Invocation != OrdinaryInvocation && actual == formal) {
			return false
		}
		id := formal.Type.ID()
		family := sqltype.PolymorphicFamily(id)
		if family == 0 {
			if id == 2249 && !formal.Type.IsComposite() {
				return actual.Type.BaseType().IsComposite() || actual.Type.IsDeferredRecord()
			}
			expected := formal
			if expected.Collation.ID != 0 && actual.Collation.ID != 0 {
				expected.Collation = actual.Collation
			}
			return expected == actual
		}
		typ := actual.Type
		identity := typ
		switch id {
		case 2277, 5078:
			if !typ.IsArray() || array[family] != nil && array[family] != typ {
				return false
			}
			array[family] = typ
			identity = typ.Element()
		case 3831, 5080, 4537, 4538:
			if (id == 3831 || id == 5080) && !typ.IsRange() || (id == 4537 || id == 4538) && !typ.IsMultirange() {
				return false
			}
			r := typ.RangeType()
			if r == nil || r.MultirangeType() == nil || r.MultirangeType().RangeType() != r ||
				sqltype.RangeInspectResult(sqltype.RangeConstruct, r) != r.MultirangeType() ||
				typ.IsMultirange() && r.MultirangeType() != typ ||
				ranged[family] != nil && ranged[family] != r {
				return false
			}
			ranged[family] = r
			identity = sqltype.RangeInspectResult(sqltype.RangeLower, typ)
		default:
			// Derived OUT members project the input family solution; they do
			// not impose scalar-result or actual-input category restrictions.
			if !outputMember && ((id == 2776 || id == 5079) && typ.BaseType().IsArray() || id == 3500 && !typ.BaseType().IsEnum()) {
				return false
			}
		}
		if identity == nil || leaf[family] != nil && leaf[family] != identity {
			return false
		}
		leaf[family] = identity
		return true
	}
	for i, in := range d.Inputs {
		if !check(in, s.Inputs[i], false) {
			return false
		}
	}
	if !d.DerivedResult() {
		return check(d.Result, s.Result, false)
	}
	if !d.MatchesOutputTuple(d.Result.Type) || s.Result.Type == nil || s.Result.Type.ID() != 2249 || !s.Result.Type.IsComposite() || s.Result.Type.CompositeLen() != len(d.OutputFormals) || !s.Result.Valid() {
		return false
	}
	for i := range d.OutputFormals {
		formal, actual := d.Result.Type.CompositeMember(i), s.Result.Type.CompositeMember(i)
		if formal.Attribute != actual.Attribute || formal.Name != actual.Name || !check(formal.Usage, actual.Usage, true) {
			return false
		}
	}
	return true
}

// Specialize creates an invocation-private declaration projection, leaving the
// published formals and their ordered output tuple untouched.
func (d *Definition) Specialize(s InvocationSignature) (*Definition, error) {
	if !s.ValidFor(d) {
		return nil, fmt.Errorf("invalid routine invocation signature")
	}
	changed := s.Result != d.Result
	for i := range s.Inputs {
		changed = changed || s.Inputs[i] != d.Inputs[i]
	}
	if !changed {
		return d, nil
	}
	private := *d
	private.Inputs = append([]sqltype.Usage(nil), s.Inputs...)
	private.Result = s.Result
	private.Formals = append([]Formal(nil), d.Formals...)
	for i, index := range d.InputFormals {
		private.Formals[index].Usage = s.Inputs[i]
	}
	for i, index := range d.OutputFormals {
		usage := s.Result
		if d.DerivedResult() {
			usage = s.Result.Type.CompositeMember(i).Usage
		}
		private.Formals[index].Usage = usage
	}
	return &private, nil
}
