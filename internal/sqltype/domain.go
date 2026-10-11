package sqltype

import (
	"context"
	"fmt"
)

// Domain creates immutable logical type authority over an immediate base usage.
// Catalog ownership and dependencies remain the engine's responsibility.
func Domain(id ID, name, declaration string, base Usage) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" || !base.Valid() || !base.Type.Concrete() || base.Type.BaseType().id == Void || base.Type.BaseType().id == Char {
		return nil, fmt.Errorf("invalid domain descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: base.Type.BaseType().database, base: base}, nil
}

// RebindDomain reowns a concrete domain's base without losing its immutable
// identity, inventory provenance or associated-array relationship.
func (d *Descriptor) RebindDomain(base Usage, declaration string) (*Descriptor, error) {
	if !d.IsDomain() || !d.Concrete() {
		return nil, fmt.Errorf("invalid domain rebind")
	}
	validated, err := Domain(d.id, d.name, declaration, base)
	if err != nil {
		return nil, err
	}
	*validated = *d
	validated.base, validated.database, validated.sql = base, base.Type.BaseType().database, declaration
	return validated, nil
}

// LookupDomain preserves catalog base identity without admitting stored values.
// The caller supplies an already validated catalog base generation.
func LookupDomain(id ID, name, declaration string, base Usage) *Descriptor {
	return &Descriptor{id: id, name: name, sql: declaration,
		database: base.Type.BaseType().database, base: base, lookupOnly: true}
}

func (d *Descriptor) IsDomain() bool    { return d != nil && d.base.Type != nil }
func (d *Descriptor) DomainBase() Usage { return d.base }

// BaseType is physical codec authority, never logical/catalog identity.
func (d *Descriptor) BaseType() *Descriptor {
	for d.IsDomain() {
		d = d.base.Type
	}
	return d
}

func (u Usage) BaseUsage() Usage {
	for u.Type.IsDomain() {
		collation := u.Collation
		u = u.Type.base
		if collation.ID != 0 {
			u.Collation = collation
		}
	}
	return u
}

// BaseValue unwraps only the immutable tag. The scalar payload stays inline.
func (v Value) BaseValue() (Value, error) {
	if !v.typ.Concrete() {
		return Value{}, fmt.Errorf("invalid domain value")
	}
	v.typ = v.typ.BaseType()
	return v, nil
}

func (d *Descriptor) Wrap(v Value) (Value, error) {
	if !d.IsDomain() || v.typ != d.BaseType() {
		return Value{}, fmt.Errorf("foreign domain base value")
	}
	v.typ = d
	return v, nil
}

func (p CoercionPlan) Apply(ctx context.Context, v Value, input InputEnvironment, output OutputEnvironment) (Value, error) {
	if p.logicalSource == nil {
		return p.applyBase(ctx, v, input, output)
	}
	if v.typ != p.logicalSource {
		return Value{}, fmt.Errorf("foreign domain coercion value")
	}
	v.typ = p.source.Type
	value, err := p.applyBase(ctx, v, input, output)
	if err != nil {
		return Value{}, err
	}
	if value.typ != p.target.Type {
		return Value{}, fmt.Errorf("invalid domain coercion result")
	}
	value.typ = p.logicalTarget
	return value, nil
}
