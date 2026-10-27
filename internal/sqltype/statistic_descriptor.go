package sqltype

import (
	"fmt"
	"slices"
	"strconv"
)

// Only builtin construction grants this fixed catalog result authority.
// Generic Composite and Array admission remain unchanged.
func installStatisticDescriptors(types map[ID]*Descriptor) {
	row, array := types[10029], types[10028]
	members := make([]CompositeMember, 0, 31)
	add := func(name string, id ID) {
		d := types[id]
		if d == nil {
			panic("missing pg_statistic member type")
		}
		members = append(members, CompositeMember{Attribute: int32(len(members) + 1), Name: name, Usage: d.Usage()})
	}
	add("starelid", Oid)
	add("staattnum", Int2)
	add("stainherit", Bool)
	add("stanullfrac", Float4)
	add("stawidth", Int4)
	add("stadistinct", Float4)
	for _, group := range []struct {
		name string
		id   ID
	}{{"stakind", Int2}, {"staop", Oid}, {"stacoll", Oid}, {"stanumbers", 1021}, {"stavalues", 2277}} {
		for i := 1; i <= 5; i++ {
			add(group.name+strconv.Itoa(i), group.id)
		}
	}
	attributes := make(map[int32]int, len(members))
	for i, m := range members {
		attributes[m.Attribute] = i
	}
	row.composite = &compositeDefinition{members: members, attributes: attributes}
	row.lookupOnly = true
	row.catalogNullOwner = row
	array.element = row
	array.lookupOnly = true
	array.catalogNullOwner = array
	array.sql = "pg_statistic[]"
}

// RebindStatisticDescriptors preserves the authentic fixed catalog shape when
// replacement retains selected destination builtin member generations.
func RebindStatisticDescriptors(row, array *Descriptor, usages []Usage) (*Descriptor, *Descriptor, error) {
	if row == nil || row.id != 10029 || !row.Usage().CatalogNullUsage() ||
		array == nil || array.id != 10028 || !array.Usage().CatalogNullUsage() || array.element != row ||
		len(usages) != row.CompositeLen() {
		return nil, nil, fmt.Errorf("invalid catalog statistic description rebind")
	}
	changed := false
	for i, usage := range usages {
		member := row.CompositeMember(i)
		if usage.Type == nil || usage.Type.ID() != member.Usage.Type.ID() ||
			!usage.Type.IsBuiltin() || usage.Type.NamespaceName() != "pg_catalog" ||
			usage != usage.Type.Usage() || !usage.Type.Concrete() && !usage.CatalogNullUsage() {
			return nil, nil, fmt.Errorf("invalid catalog statistic member rebind")
		}
		changed = changed || usage != member.Usage
	}
	if !changed {
		return row, array, nil
	}
	rebuiltRow := *row
	rebuiltRow.composite = &compositeDefinition{members: slices.Clone(row.composite.members), attributes: row.composite.attributes}
	for i, usage := range usages {
		rebuiltRow.composite.members[i].Usage = usage
	}
	rebuiltRow.catalogNullOwner = &rebuiltRow
	rebuiltArray := *array
	rebuiltArray.element = &rebuiltRow
	rebuiltArray.catalogNullOwner = &rebuiltArray
	return &rebuiltRow, &rebuiltArray, nil
}
