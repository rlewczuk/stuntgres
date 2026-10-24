package sqltype

import (
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/sqlstate"
)

// EnumMember keeps datum identity and catalog sort position independent of rank.
// Definitions are immutable; callers receive owned copies of the member slice.
type EnumMember struct {
	ID        int64
	Label     string
	SortOrder float32
}
type enumDefinition struct {
	members []EnumMember
	byLabel map[string]int64
	ranks   map[int64]int
}

func Enum(id ID, name, declaration string, labels []string) (*Descriptor, error) {
	members := make([]EnumMember, len(labels))
	for i, label := range labels {
		members[i] = EnumMember{ID: int64(i)*2 + 1, Label: label, SortOrder: float32(i + 1)}
	}
	return EnumWithMembers(id, name, declaration, members)
}

func EnumWithMembers(id ID, name, declaration string, members []EnumMember) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid enum descriptor")
	}
	definition := &enumDefinition{members: slices.Clone(members), byLabel: make(map[string]int64, len(members)), ranks: make(map[int64]int, len(members))}
	var lastEven int64
	for i, member := range definition.members {
		if member.ID <= 0 || member.ID > math.MaxUint32 || len(member.Label) > 63 {
			return nil, fmt.Errorf("invalid enum member")
		}
		if math.IsNaN(float64(member.SortOrder)) || math.IsInf(float64(member.SortOrder), 0) || i > 0 && definition.members[i-1].SortOrder >= member.SortOrder {
			return nil, fmt.Errorf("invalid enum sort position")
		}
		if member.ID%2 == 0 {
			if member.ID <= lastEven {
				return nil, fmt.Errorf("invalid even enum member order")
			}
			lastEven = member.ID
		}
		if err := ValidateTextInput(member.Label); err != nil {
			return nil, err
		}
		if _, ok := definition.byLabel[member.Label]; ok {
			return nil, fmt.Errorf("duplicate enum label")
		}
		if _, ok := definition.ranks[member.ID]; ok {
			return nil, fmt.Errorf("duplicate enum member identity")
		}
		definition.byLabel[member.Label] = member.ID
		definition.ranks[member.ID] = i
	}
	return &Descriptor{id: id, name: name, sql: declaration, enum: definition}, nil
}
func (d *Descriptor) IsEnum() bool { return d != nil && d.enum != nil }
func (d *Descriptor) IsUserDefined() bool {
	return d != nil && !d.IsBuiltin() && (d.IsDomain() || !d.lookupOnly && (d.IsEnum() || d.IsComposite() || d.IsQueryInt() || d.IsCube() || d.IsCitext() || d.IsEan13() || d.IsIsbn() || d.IsRange() || d.IsMultirange()))
}
func (d *Descriptor) EnumLabels() []string {
	if !d.IsEnum() {
		return nil
	}
	labels := make([]string, len(d.enum.members))
	for i, m := range d.enum.members {
		labels[i] = m.Label
	}
	return labels
}
func (d *Descriptor) EnumMembers() []EnumMember {
	if !d.IsEnum() {
		return nil
	}
	return slices.Clone(d.enum.members)
}

// EnumMemberCount and EnumMemberAt permit allocation-free immutable inspection.
func (d *Descriptor) EnumMemberCount() int {
	if !d.IsEnum() {
		return 0
	}
	return len(d.enum.members)
}
func (d *Descriptor) EnumMemberAt(index int) (EnumMember, bool) {
	if !d.IsEnum() || index < 0 || index >= len(d.enum.members) {
		return EnumMember{}, false
	}
	return d.enum.members[index], true
}
func (d *Descriptor) EnumMemberID(label string) (int64, bool) {
	if !d.IsEnum() {
		return 0, false
	}
	id, ok := d.enum.byLabel[label]
	return id, ok
}
func (v Value) EnumMemberID() (int64, bool) {
	if v.null || v.typ == nil || !v.typ.BaseType().IsEnum() {
		return 0, false
	}
	_, ok := v.typ.BaseType().enum.ranks[v.integer]
	return v.integer, ok
}
func (d *Descriptor) enumDiagnosticName(env InputEnvironment) string {
	if env.TypeDiagnosticName != nil {
		if name := env.TypeDiagnosticName(d.id); name != "" {
			return name
		}
	}
	return d.DiagnosticName()
}
func (d *Descriptor) checkEnumMember(id int64, env InputEnvironment) error {
	if env.EnumMemberSafe == nil {
		return nil
	}
	if env.EnumMemberSafe(d.id, id) {
		return nil
	}
	rank, ok := d.enum.ranks[id]
	if !ok {
		return fmt.Errorf("invalid enum member")
	}
	err := failure("55P04", fmt.Sprintf(`unsafe use of new value "%s" of enum type %s`, d.enum.members[rank].Label, d.enumDiagnosticName(env))).(*sqlstate.Error)
	err.Hint = "New enum values must be committed before they can be used."
	return err
}
func (d *Descriptor) enumInput(text string, env InputEnvironment) (Value, error) {
	id, ok := d.enum.byLabel[text]
	if !ok {
		return Value{}, failure("22P02", fmt.Sprintf(`invalid input value for enum %s: "%s"`, d.enumDiagnosticName(env), text))
	}
	if err := d.checkEnumMember(id, env); err != nil {
		return Value{}, err
	}
	return Value{typ: d, integer: id}, nil
}
func (d *Descriptor) enumOutput(v Value) (string, error) {
	rank, ok := d.enum.ranks[v.integer]
	if v.typ != d || v.null || !ok {
		return "", fmt.Errorf("invalid enum value")
	}
	return d.enum.members[rank].Label, nil
}

// RemapEnum is only for callers holding an explicit whole-catalog identity map.
// Ordinary same-lifetime Retag instead checks stable member membership.
func (v Value) RemapEnum(target *Descriptor) (Value, error) {
	if !v.typ.IsEnum() || !target.IsEnum() {
		return Value{}, fmt.Errorf("incompatible imported enum")
	}
	if !v.null {
		label, err := v.typ.enumOutput(v)
		if err != nil {
			return Value{}, err
		}
		id, ok := target.enum.byLabel[label]
		if !ok {
			return Value{}, fmt.Errorf("incompatible imported enum")
		}
		v.integer = id
	}
	v.typ = target
	return v, nil
}
