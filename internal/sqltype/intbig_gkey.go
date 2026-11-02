package sqltype

import "fmt"

// IntbigGkey is the genuine extension-allocated GiST support-key identity.
// Its released public I/O deliberately rejects nonnull keys. Key producers and
// payload algorithms belong to the index provider, not an alternate text codec.
func IntbigGkey(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid intbig_gkey descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "INTBIG_GKEY", intbigGkey: true}, nil
}

func (d *Descriptor) IsIntbigGkey() bool { return d != nil && d.intbigGkey }

// RemapIntbigGkey is used only after catalog import has authorized both roles.
// No nonnull public payload exists; never turn an arbitrary value into a key.
func (v Value) RemapIntbigGkey(target *Descriptor) (Value, error) {
	if v.typ == nil || !v.typ.IsIntbigGkey() || !target.IsIntbigGkey() || !v.null {
		return Value{}, fmt.Errorf("invalid imported intbig_gkey value")
	}
	v.typ = target
	return v, nil
}
