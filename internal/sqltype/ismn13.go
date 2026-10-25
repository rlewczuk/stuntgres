package sqltype

import "fmt"

// Ismn13 owns the ISMN family with independently identified long-form output.
func Ismn13(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid ismn13 descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISMN13", ismn13: true}, nil
}

func (d *Descriptor) IsIsmn13() bool { return d != nil && d.ismn13 }

func (v Value) validIsmn13() bool {
	return v.typ != nil && v.typ.BaseType().IsIsmn13() && validIsmnNumber(v.integer)
}

// RemapIsmn13 requires source-pointer authorization from the catalog importer.
func (v Value) RemapIsmn13(target *Descriptor) (Value, error) {
	if !v.typ.IsIsmn13() || !target.IsIsmn13() || !v.null && !v.validIsmn13() {
		return Value{}, fmt.Errorf("incompatible imported ismn13")
	}
	v.typ = target
	return v, nil
}

func (d *Descriptor) ismn13Output(v Value) (string, error) {
	if !v.validIsmn13() {
		return "", fmt.Errorf("invalid ismn13 payload")
	}
	return isnLongOutput(v.integer), nil
}
