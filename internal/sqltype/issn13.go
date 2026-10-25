package sqltype

import "fmt"

// Issn13 owns the ISSN family with independently identified long-form output.
func Issn13(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid issn13 descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISSN13", issn13: true}, nil
}

func (d *Descriptor) IsIssn13() bool { return d != nil && d.issn13 }

func (v Value) validIssn13() bool {
	return v.typ != nil && v.typ.BaseType().IsIssn13() && validIssnNumber(v.integer)
}

// RemapIssn13 requires source-pointer authorization from the catalog importer.
func (v Value) RemapIssn13(target *Descriptor) (Value, error) {
	if !v.typ.IsIssn13() || !target.IsIssn13() || !v.null && !v.validIssn13() {
		return Value{}, fmt.Errorf("incompatible imported issn13")
	}
	v.typ = target
	return v, nil
}

func (d *Descriptor) issn13Output(v Value) (string, error) {
	if !v.validIssn13() {
		return "", fmt.Errorf("invalid issn13 payload")
	}
	return isnLongOutput(v.integer), nil
}
