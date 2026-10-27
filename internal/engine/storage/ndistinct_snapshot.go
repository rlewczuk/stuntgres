package storage

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const NDistinctSnapshotMarker = "stuntgres-ndistinct-v1"

type NDistinctUsageRecord struct {
	TypeSchema      string `json:"type_schema"`
	TypeName        string `json:"type_name"`
	Typmod          int32  `json:"typmod"`
	CollationSchema string `json:"collation_schema"`
	CollationName   string `json:"collation_name"`
}

type NDistinctSnapshot struct {
	Schema         string                 `json:"schema"`
	Name           string                 `json:"name"`
	RelationSchema string                 `json:"relation_schema"`
	RelationName   string                 `json:"relation_name"`
	Keys           []int32                `json:"keys"`
	Kinds          string                 `json:"kinds"`
	Inherited      bool                   `json:"inherited"`
	Usages         []NDistinctUsageRecord `json:"usages"`
	Subsets        [][]int16              `json:"subsets"`
	Counts         []uint64               `json:"counts"`
}

// CaptureNDistinctSnapshots copies only checked construction facts. It never
// decodes display/binary data or computes statistics from current relation rows.
func CaptureNDistinctSnapshots(read catalog.Read, metadata map[ids.ObjectAddress]*RelationMetadata) []NDistinctSnapshot {
	var records []NDistinctSnapshot
	for address, entry := range metadata {
		relation, ok := read.Relation(address)
		if !ok || entry == nil {
			continue
		}
		facts := CompatibleExtendedStatistics(entry.Statistics, relation, read)
		if facts == nil {
			continue
		}
		for key, fact := range facts.Extended {
			d, ok := read.Statistics(key.Definition.Address)
			if !ok {
				continue
			}
			ns := read.MustNamespace(d.Namespace())
			if ns.Temporary != (ids.SessionToken{}) {
				continue
			}
			r := NDistinctSnapshot{
				Schema: ns.Name(), Name: d.Name(),
				RelationSchema: read.MustNamespace(relation.Namespace()).Name(),
				RelationName:   relation.Name(), Kinds: d.Selection.Kinds(), Inherited: key.Inherited,
				Keys: make([]int32, d.Selection.Len()), Usages: make([]NDistinctUsageRecord, d.Selection.Len()),
				Subsets: make([][]int16, len(fact.Items)), Counts: make([]uint64, len(fact.Items)),
			}
			for i := range d.Selection.Len() {
				r.Keys[i] = d.Selection.Key(i)
				r.Usages[i] = ndistinctUsageRecord(read, fact.Usages[i])
			}
			for i, item := range fact.Items {
				r.Subsets[i] = slices.Clone(item.Attributes)
				r.Counts[i] = uint64(item.Value)
			}
			records = append(records, r)
		}
	}
	slices.SortFunc(records, func(a, b NDistinctSnapshot) int {
		if n := strings.Compare(a.Schema, b.Schema); n != 0 {
			return n
		}
		if n := strings.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		if a.Inherited == b.Inherited {
			return 0
		}
		if a.Inherited {
			return 1
		}
		return -1
	})
	return records
}

// Validate requires every canonical subset exactly once, in released order.
func (r NDistinctSnapshot) Validate() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ndistinct snapshot") }
	if r.Schema == "" || r.Name == "" || r.RelationSchema == "" || r.RelationName == "" ||
		len(r.Keys) < 2 || len(r.Keys) > 8 || len(r.Usages) != len(r.Keys) || !strings.Contains(r.Kinds, "d") {
		return invalid()
	}
	selection, err := catalog.NewStatisticsSelection(r.Keys, r.Kinds)
	if err != nil || selection.Kinds() != r.Kinds {
		return invalid()
	}
	for i, key := range r.Keys {
		if key > 32767 || selection.Key(i) != key || r.Usages[i].TypeSchema == "" || r.Usages[i].TypeName == "" {
			return invalid()
		}
	}
	expected := (1 << len(r.Keys)) - len(r.Keys) - 1
	if len(r.Subsets) != expected || len(r.Counts) != expected {
		return invalid()
	}
	index := 0
	var attributes [8]int16
	var choose func(int, int, int) error
	choose = func(start, left, width int) error {
		if left == 0 {
			if !slices.Equal(attributes[:width], r.Subsets[index]) || r.Counts[index] == 0 || r.Counts[index] > 2147483647 {
				return invalid()
			}
			index++
			return nil
		}
		for i := start; i <= len(r.Keys)-left; i++ {
			attributes[width] = int16(r.Keys[i])
			if err := choose(i+1, left-1, width+1); err != nil {
				return err
			}
		}
		return nil
	}
	for size := 2; size <= len(r.Keys); size++ {
		if err := choose(0, size, 0); err != nil {
			return err
		}
	}
	return nil
}

// RestoreNDistinctSnapshots constructs destination-owned typed facts using only
// checked names, stable keys and usages. No source OID grants authority.
func RestoreNDistinctSnapshots(read catalog.Read, records []NDistinctSnapshot) (map[ids.ObjectAddress]*RelationMetadata, error) {
	var metadata map[ids.ObjectAddress]*RelationMetadata
	seen := make(map[ExtendedStatisticsKey]bool, len(records))
	for _, r := range records {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		invalid := func() (map[ids.ObjectAddress]*RelationMetadata, error) {
			return nil, fmt.Errorf("stuntgres: incompatible ndistinct snapshot")
		}
		ns := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: r.Schema})
		d, ok := read.Statistics(read.NameAddress(ids.NameKey{ClassID: ids.StatisticExtClassID, Namespace: ns, Name: r.Name}))
		if !ok {
			return invalid()
		}
		relation, ok := read.Relation(d.Relation.Address)
		if !ok || relation.Name() != r.RelationName || read.MustNamespace(relation.Namespace()).Name() != r.RelationSchema ||
			d.Selection.Kinds() != r.Kinds || d.Selection.Len() != len(r.Keys) || relation.Partition != nil && !r.Inherited {
			return invalid()
		}
		key := ExtendedStatisticsKey{Definition: read.Domain().Ref(read.Epoch(), d.Address()), Inherited: r.Inherited}
		if seen[key] {
			return invalid()
		}
		seen[key] = true
		fact := &ExtendedStatistics{Relation: d.Relation, Selection: d.Selection}
		for i, attribute := range r.Keys {
			if d.Selection.Key(i) != attribute {
				return invalid()
			}
			slot := relation.IndexForAttribute(attribute)
			if slot < 0 || ndistinctUsageRecord(read, relation.Columns[slot].Usage) != r.Usages[i] {
				return invalid()
			}
			fact.Usages[i] = relation.Columns[slot].Usage
		}
		fact.Items = make([]sqltype.StatisticsItem, len(r.Subsets))
		for i, attrs := range r.Subsets {
			fact.Items[i] = sqltype.StatisticsItem{Attributes: slices.Clone(attrs), Value: float64(r.Counts[i])}
		}
		typ, ok := read.LookupType(sqltype.PgNDistinct)
		if !ok {
			return invalid()
		}
		var err error
		fact.Value, err = sqltype.StatisticsValue(typ, fact.Items)
		if err != nil {
			return nil, err
		}
		if r.Inherited {
			fact.Hierarchy, err = restoredNDistinctHierarchy(read, relation)
			if err != nil {
				return nil, err
			}
		}
		if metadata == nil {
			metadata = make(map[ids.ObjectAddress]*RelationMetadata)
		}
		entry := metadata[relation.Address()]
		if entry == nil {
			entry = &RelationMetadata{}
			metadata[relation.Address()] = entry
		}
		entry.Statistics = MergeExtendedStatistics(entry.Statistics, map[ExtendedStatisticsKey]*ExtendedStatistics{key: fact})
	}
	return metadata, nil
}

func restoredNDistinctHierarchy(read catalog.Read, relation *catalog.Relation) (*StatisticsHierarchy, error) {
	visited := make(map[ids.ObjectAddress]bool)
	var closure []*catalog.Relation
	var visit func(*catalog.Relation) error
	visit = func(rel *catalog.Relation) error {
		if visited[rel.Address()] {
			return nil
		}
		visited[rel.Address()] = true
		closure = append(closure, rel)
		children := catalog.InheritanceChildren(read, rel.Address())
		if rel.Partition != nil {
			for _, address := range rel.Partition.Children {
				child, ok := read.Relation(address)
				if !ok {
					return fmt.Errorf("stuntgres: missing ndistinct hierarchy")
				}
				children = append(children, child)
			}
		}
		for _, child := range children {
			if child.Foreign == nil {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(relation); err != nil {
		return nil, err
	}
	if len(closure) < 2 && relation.Partition == nil {
		return nil, fmt.Errorf("stuntgres: incompatible ndistinct hierarchy")
	}
	return &StatisticsHierarchy{Relations: closure}, nil
}

func ndistinctUsageRecord(read catalog.Read, u sqltype.Usage) NDistinctUsageRecord {
	typeDescriptor, ok := read.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())})
	if !ok {
		return NDistinctUsageRecord{}
	}
	r := NDistinctUsageRecord{
		TypeSchema: read.MustNamespace(typeDescriptor.Namespace()).Name(), TypeName: typeDescriptor.Name(), Typmod: u.Typmod,
	}
	if u.Collation.ID != 0 {
		if d, ok := read.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}); ok {
			r.CollationSchema = read.MustNamespace(d.Namespace()).Name()
			r.CollationName = d.Name()
		}
	}
	return r
}
