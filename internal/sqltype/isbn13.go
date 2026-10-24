package sqltype

import "fmt"

// Isbn13 owns the ISBN family with independently identified long-form output.
func Isbn13(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" { return nil, fmt.Errorf("invalid isbn13 descriptor") }
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISBN13", isbn13: true}, nil
}

func (d *Descriptor) IsIsbn13() bool { return d != nil && d.isbn13 }

func (v Value) validIsbn13() bool {
	return v.typ != nil && v.typ.BaseType().IsIsbn13() && validIsbnNumber(v.integer)
}

// RemapIsbn13 requires source-pointer authorization from the catalog importer.
func (v Value) RemapIsbn13(target *Descriptor) (Value, error) {
	if !v.typ.IsIsbn13() || !target.IsIsbn13() || !v.null && !v.validIsbn13() { return Value{}, fmt.Errorf("incompatible imported isbn13") }
	v.typ = target
	return v, nil
}

func (d *Descriptor) isbn13Output(v Value) (string, error) {
	if !v.validIsbn13() { return "", fmt.Errorf("invalid isbn13 payload") }
	return isnLongOutput(v.integer), nil
}
