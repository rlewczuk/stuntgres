package catalog

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Cast has no owner, ACL or namespace. Its endpoints are its identity.
type CastPair struct{ Source, Target sqltype.ID }
type Cast struct {
	OID                   ids.OID
	ObjectRevision        ids.DefinitionRevision
	Source, Target        sqltype.ID
	Function              ids.ObjectAddress
	InputCast, ResultCast ids.ObjectAddress
	Context, Method       string
	GeneratedRange        ids.ObjectAddress // Canonical range owner; zero for ordinary user casts.
}

func (d *Cast) Address() ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: d.OID}
}
func (d *Cast) Revision() ids.DefinitionRevision { return d.ObjectRevision }
func (*Cast) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *Cast) Name() string                     { return fmt.Sprintf("%d,%d", d.Source, d.Target) }
func (*Cast) Kind() ids.ObjectKind               { return ids.CastObject }
func (d *Cast) Pair() CastPair                   { return CastPair{d.Source, d.Target} }
func (r Read) Cast(source, target sqltype.ID) (*Cast, bool) {
	if r.state == nil {
		return nil, false
	}
	d, ok := r.state.castPairs[CastPair{source, target}]
	return d, ok
}
func (s *State) updateCastIndex(old, next Descriptor) {
	a, was := old.(*Cast)
	b, is := next.(*Cast)
	if !was && !is {
		return
	}
	s.castPairs = maps.Clone(s.castPairs)
	if s.castPairs == nil {
		s.castPairs = make(map[CastPair]*Cast)
	}
	if was {
		delete(s.castPairs, a.Pair())
	}
	if is {
		s.castPairs[b.Pair()] = b
	}
}
func (c *Candidate) Cast(source, target sqltype.ID) (*Cast, bool) {
	a := c.LookupName(ids.NameKey{ClassID: ids.CastClassID, Name: fmt.Sprintf("%d,%d", source, target)})
	if a.ObjectID == 0 {
		return nil, false
	}
	if d, ok := c.descriptors[a].(*Cast); ok {
		return d, true
	}
	d, ok := c.Base.Get(a).(*Cast)
	return d, ok
}

// CastDependencies is shared by pg_depend, DROP closure and dump ordering.
func CastDependencies(d *Cast) []ids.ObjectAddress {
	out := []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Source)}, {ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Target)}}
	for _, a := range []ids.ObjectAddress{d.Function, d.InputCast, d.ResultCast} {
		if a.ObjectID != 0 {
			out = append(out, a)
		}
	}
	return out
}

func CastDependencyKind(d *Cast) byte {
	if d.GeneratedRange != (ids.ObjectAddress{}) {
		return 'i'
	}
	return 'n'
}

type CastProvider struct{}

func (CastProvider) ClassID() ids.OID { return ids.CastClassID }
func (CastProvider) Validate(v Read, descriptor Descriptor) error {
	d, ok := descriptor.(*Cast)
	if !ok || d == nil || d.OID == 0 || d.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid cast descriptor")
	}
	if d.Context != "e" && d.Context != "a" && d.Context != "i" {
		return fmt.Errorf("stuntgres: invalid cast context")
	}
	if d.Method != "f" && d.Method != "i" && d.Method != "b" {
		return fmt.Errorf("stuntgres: invalid cast method")
	}
	if (d.Method == "f") != (d.Function.ObjectID != 0) {
		return fmt.Errorf("stuntgres: invalid cast function")
	}
	for _, a := range CastDependencies(d) {
		if v.Get(a) == nil {
			return fmt.Errorf("stuntgres: unavailable cast dependency")
		}
	}
	source, sourceOK := v.LookupType(d.Source)
	target, targetOK := v.LookupType(d.Target)
	if !sourceOK || !targetOK {
		return fmt.Errorf("stuntgres: missing cast endpoint")
	}
	if d.Method == "b" {
		if _, err := sqltype.PlanBinaryCoercion(source, target); err != nil {
			return err
		}
	}
	if d.Method == "f" {
		p, ok := v.Procedure(d.Function)
		if !ok || p.RoutineKind != FunctionRoutine || p.Builtin != nil && p.Builtin.ReturnsSet {
			return fmt.Errorf("stuntgres: invalid cast routine")
		}
		words := strings.Split(string(p.Signature), ",")
		if p.Signature == "" || len(words) > 3 {
			return fmt.Errorf("stuntgres: invalid cast routine arity")
		}
		var inputs [3]sqltype.ID
		for i, word := range words {
			id, err := strconv.ParseUint(word, 10, 32)
			if err != nil {
				return err
			}
			inputs[i] = sqltype.ID(id)
		}
		input, ok := ImplicitBinaryCast(v, d.Source, inputs[0])
		if !ok || input != d.InputCast {
			return fmt.Errorf("stuntgres: invalid cast input adapter")
		}
		output, ok := ImplicitBinaryCast(v, sqltype.ID(p.Result), d.Target)
		if !ok || output != d.ResultCast {
			return fmt.Errorf("stuntgres: invalid cast result adapter")
		}
		if len(words) > 1 && inputs[1] != sqltype.Int4 || len(words) > 2 && inputs[2] != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid cast auxiliary arguments")
		}
	}
	if d.GeneratedRange != (ids.ObjectAddress{}) {
		owner, ok := v.Type(d.GeneratedRange)
		procedure, present := v.Procedure(d.Function)
		if !ok || owner.SQLType != source || !source.IsRange() || source.MultirangeType() != target || target.RangeType() != source ||
			d.GeneratedRange != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Source)}) ||
			d.Context != "e" || d.Method != "f" || d.InputCast != (ids.ObjectAddress{}) || d.ResultCast != (ids.ObjectAddress{}) ||
			!present || procedure.RangeConstructor == nil || procedure.RangeConstructor.Form != MultirangeConstructorOne || procedure.RangeConstructor.Type != target {
			return fmt.Errorf("stuntgres: invalid generated range cast")
		}
		if err := ValidateRangeConstructor(v, procedure); err != nil {
			return err
		}
	}
	if selected, ok := v.Cast(d.Source, d.Target); !ok || selected != d {
		return fmt.Errorf("stuntgres: invalid cast pair index")
	}
	return nil
}

func (r Read) HasCasts() bool { return r.state != nil && len(r.state.castPairs) != 0 }

// ImplicitBinaryCast selects only a direct implicit relabel or domain/base
// identity. Function-signature validation and provider validation share it.
func ImplicitBinaryCast(v Read, source, target sqltype.ID) (ids.ObjectAddress, bool) {
	if source == target {
		return ids.ObjectAddress{}, true
	}
	if typ, ok := v.LookupType(source); ok && typ.IsDomain() {
		return ImplicitBinaryCast(v, typ.BaseType().ID(), target)
	}
	if d, ok := v.Cast(source, target); ok {
		return d.Address(), d.Method == "b" && d.Context == "i"
	}
	for i := range sqltype.CastMetadataCount() {
		d, _ := sqltype.CastMetadataAt(i)
		if d.Source == source && d.Target == target {
			return ids.ObjectAddress{}, d.Method == 'b' && d.Context == 'i'
		}
	}
	return ids.ObjectAddress{}, false
}
