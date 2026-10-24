package sqltype

import "fmt"

// Citext is a catalog-generation-owned string scalar. Input and output preserve
// spelling; its existence grants no comparison or array storage authority.
func Citext(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid citext descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "CITEXT", citext: true}, nil
}

func (d *Descriptor) IsCitext() bool { return d != nil && d.citext }

// RemapCitext is used only by source-pointer-authorized catalog imports. Ordinary
// Retag and coercion cannot cross citext generations, even with equal OIDs.
func (v Value) RemapCitext(target *Descriptor) (Value, error) {
	if !v.typ.IsCitext() || !target.IsCitext() {
		return Value{}, fmt.Errorf("incompatible imported citext")
	}
	v.typ = target
	return v, nil
}

func (p CoercionPlan) planCitext() (CoercionPlan, error) {
	from, to := p.source.Type, p.target.Type
	if !from.Concrete() || !to.Concrete() {
		return p, failure("42804", "incompatible source and destination types")
	}
	switch {
	case from == to:
		return p, nil
	case from.IsCitext() && to.IsCitext():
		// Numeric/name coincidence is not catalog-generation authority.
	case to.IsCitext() && p.context >= Assignment:
		switch from.id {
		case Text, VarChar, BPChar:
			p.method = stringConversion
		case Bool:
			p.method = booleanText
		case Inet:
			p.method = networkTextConversion
		default:
			p.method = viaIO
		}
		return p, nil
	case from.IsCitext() && (to.id == Text || to.id == VarChar || to.id == BPChar && p.context >= Assignment):
		p.method = stringConversion
		return p, nil
	case from.IsCitext() && (p.context == Explicit || p.context >= Assignment && castStringCategory(to)):
		if to.id == Void {
			return p, failure("0A000", "input of type void is not supported")
		}
		p.method = viaIO
		return p, nil
	}
	return p, failure("42804", "incompatible source and destination types")
}
