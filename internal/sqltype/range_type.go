package sqltype

import (
	"fmt"
	"strings"
)

// RangeDefinition captures existing executable subtype ordering, not a callback
// or a name-based provider. Catalog owners validate its current generations.
type RangeDefinition struct {
	Subtype  Usage
	Class    IndexClass
	Ordering IndexComparison
	Diff     uint32
}

func (d *Descriptor) IsRange() bool { return d != nil && !d.IsDomain() && d.rangeSubtype != nil }
func (d *Descriptor) IsMultirange() bool {
	return d != nil && !d.IsDomain() && d.multirangeRange != nil
}
func (d *Descriptor) RangeType() *Descriptor {
	if d.IsMultirange() {
		return d.multirangeRange
	}
	if d.IsRange() {
		return d
	}
	return nil
}
func (d *Descriptor) MultirangeType() *Descriptor {
	if d.IsRange() {
		return d.multirangeCompanion
	}
	return nil
}
func (d *Descriptor) RangeDefinition() (RangeDefinition, bool) {
	if d == nil || d.customRange == nil {
		return RangeDefinition{}, false
	}
	return *d.customRange, true
}

// NewRangeFamily publishes reciprocal immutable identities together. Custom
// ranges have no canonical function even when their subtype is discrete.
func NewRangeFamily(id ID, name, sql string, multiID ID, multiName, multiSQL string, definition RangeDefinition, boolean *Descriptor) (*Descriptor, *Descriptor, error) {
	if id == 0 || multiID == 0 || id == multiID || name == "" || multiName == "" || sql == "" || multiSQL == "" || !definition.Subtype.Valid() || !definition.Class.Accepts(definition.Subtype.Type) || definition.Class.MethodOID() != 403 || definition.Class.IsHash() || definition.Class.IsGiST() || definition.Ordering.Support() != definition.Class.Support(1) || definition.Ordering.Support().Member == 0 {
		return nil, nil, fmt.Errorf("invalid custom range definition")
	}
	expected, err := BindIndexOrdering(definition.Subtype, definition.Class)
	if err != nil || expected != definition.Ordering || boolean == nil || boolean.ID() != Bool || !boolean.Concrete() {
		return nil, nil, fmt.Errorf("invalid custom range provider capture")
	}
	r := &Descriptor{id: id, name: name, sql: sql, database: strings.ToUpper(name), rangeSubtype: definition.Subtype.Type, rangeBoolean: boolean, customRange: &definition}
	m := &Descriptor{id: multiID, name: multiName, sql: multiSQL, database: strings.ToUpper(multiName), multirangeRange: r}
	r.multirangeCompanion = m
	return r, m, nil
}
