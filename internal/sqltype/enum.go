package sqltype

import (
	"fmt"
	"slices"
)

// enumDefinition is constructed once and never exposes its lookup or labels.
type enumDefinition struct {
	labels   []string
	ordinals map[string]int64
}

// Enum creates a distinct logical scalar, ordered by its declaration.
func Enum(id ID, name, declaration string, labels []string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid enum descriptor")
	}
	definition := &enumDefinition{labels: slices.Clone(labels), ordinals: make(map[string]int64, len(labels))}
	for i, label := range definition.labels {
		if len(label) > 63 {
			return nil, fmt.Errorf("invalid enum label length")
		}
		if err := validateCharacterInput(label); err != nil {
			return nil, err
		}
		if _, exists := definition.ordinals[label]; exists {
			return nil, fmt.Errorf("duplicate enum label")
		}
		definition.ordinals[label] = int64(i)
	}
	return &Descriptor{id: id, name: name, sql: declaration, enum: definition}, nil
}
func (d *Descriptor) IsEnum() bool { return d != nil && d.enum != nil }
func (d *Descriptor) IsUserDefined() bool {
	return d != nil && !d.lookupOnly && (d.IsDomain() || d.IsEnum() || d.IsComposite())
}
func (d *Descriptor) EnumLabels() []string {
	if !d.IsEnum() {
		return nil
	}
	return slices.Clone(d.enum.labels)
}
func (d *Descriptor) enumInput(text string, env InputEnvironment) (Value, error) {
	ordinal, ok := d.enum.ordinals[text]
	if !ok {
		name := d.DiagnosticName()
		if env.TypeDiagnosticName != nil {
			if captured := env.TypeDiagnosticName(d.id); captured != "" {
				name = captured
			}
		}
		return Value{}, failure("22P02", fmt.Sprintf("invalid input value for enum %s: \"%s\"", name, text))
	}
	return Value{typ: d, integer: ordinal}, nil
}
func (d *Descriptor) enumOutput(v Value) (string, error) {
	if v.typ != d || v.null || v.integer < 0 || v.integer >= int64(len(d.enum.labels)) {
		return "", fmt.Errorf("invalid enum value")
	}
	return d.enum.labels[v.integer], nil
}
func compatibleEnum(from, to *Descriptor) bool {
	return from.IsEnum() == to.IsEnum() && (!from.IsEnum() || slices.Equal(from.enum.labels, to.enum.labels))
}

// RemapEnum requires an explicit catalog identity map at the caller; labels alone
// never grant ordinary casts or Retag permission across distinct logical types.
func (v Value) RemapEnum(target *Descriptor) (Value, error) {
	if !v.typ.IsEnum() || !target.IsEnum() || !compatibleEnum(v.typ, target) {
		return Value{}, fmt.Errorf("incompatible imported enum")
	}
	if !v.null {
		if _, err := v.typ.enumOutput(v); err != nil {
			return Value{}, err
		}
	}
	v.typ = target
	return v, nil
}
