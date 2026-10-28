package storage

import (
	"context"
	"fmt"
	"math"
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

type DependencySnapshot struct {
	Attributes []int16 `json:"attributes"`
	Degree     float64 `json:"degree"`
}

type ExtendedStatisticsSnapshot struct {
	Schema         string                         `json:"schema"`
	Name           string                         `json:"name"`
	RelationSchema string                         `json:"relation_schema"`
	RelationName   string                         `json:"relation_name"`
	Keys           []int32                        `json:"keys"`
	Kinds          string                         `json:"kinds"`
	Inherited      bool                           `json:"inherited"`
	Usages         []NDistinctUsageRecord         `json:"usages"`
	Subsets        [][]int16                      `json:"subsets"`
	Counts         []uint64                       `json:"counts"`
	MCV            *ExtendedMCVSnapshot           `json:"mcv,omitempty"`
	Expressions    []StatisticsExpressionSnapshot `json:"expressions,omitempty"`
	Dependencies   *[]DependencySnapshot          `json:"dependencies,omitempty"`
}

// CaptureExtendedStatisticsSnapshots transports checked retained construction
// facts, never refreshed rows. Column-only records retain their original bytes.
func CaptureExtendedStatisticsSnapshots(ctx context.Context, read catalog.Read, metadata map[ids.ObjectAddress]*RelationMetadata, codec StatisticsSnapshotCodec) ([]ExtendedStatisticsSnapshot, error) {
	var records []ExtendedStatisticsSnapshot
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
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			d, ok := read.Statistics(key.Definition.Address)
			if !ok {
				continue
			}
			ns := read.MustNamespace(d.Namespace())
			if ns.Temporary != (ids.SessionToken{}) {
				continue
			}
			r := ExtendedStatisticsSnapshot{
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
			if strings.Contains(r.Kinds, "f") {
				items := make([]DependencySnapshot, len(fact.DependencyItems))
				for i, item := range fact.DependencyItems {
					items[i] = DependencySnapshot{Attributes: slices.Clone(item.Attributes), Degree: item.Value}
				}
				r.Dependencies = &items
			}
			var err error
			if fact.ExpressionLen() != 0 {
				r.Expressions, err = captureExpressionSnapshots(ctx, read, relation, fact, codec)
				if err != nil {
					return nil, err
				}
			}
			r.MCV, err = captureMCVSnapshot(ctx, fact, codec)
			if err != nil {
				return nil, err
			}
			records = append(records, r)
		}
	}
	slices.SortFunc(records, func(a, b ExtendedStatisticsSnapshot) int {
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
	return records, ctx.Err()
}

// Validate requires every canonical subset exactly once, in released order.
func (r ExtendedStatisticsSnapshot) Validate() error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid extended statistics snapshot") }
	width := len(r.Keys) + len(r.Expressions)
	if r.Schema == "" || r.Name == "" || r.RelationSchema == "" || r.RelationName == "" ||
		width < 1 || width > 8 || len(r.Usages) != len(r.Keys) || !strings.ContainsAny(r.Kinds, "defm") {
		return invalid()
	}
	selection, err := catalog.NewStatisticsSelection(r.Keys, len(r.Expressions), r.Kinds)
	if err != nil || selection.Kinds() != r.Kinds {
		return invalid()
	}
	if r.MCV != nil {
		if !strings.Contains(r.Kinds, "m") {
			return invalid()
		}
		if err := r.MCV.validate(width); err != nil {
			return err
		}
	}
	for _, expression := range r.Expressions {
		if err := expression.validate(); err != nil {
			return err
		}
	}
	for i, key := range r.Keys {
		if key > 32767 || selection.Key(i) != key || r.Usages[i].TypeSchema == "" || r.Usages[i].TypeName == "" {
			return invalid()
		}
	}
	expected := 0
	if strings.Contains(r.Kinds, "d") {
		expected = (1 << width) - width - 1
	}
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
		for i := start; i <= selection.DimensionLen()-left; i++ {
			attributes[width] = int16(selection.Dimension(i))
			if err := choose(i+1, left-1, width+1); err != nil {
				return err
			}
		}
		return nil
	}
	for size := 2; expected != 0 && size <= width; size++ {
		if err := choose(0, size, 0); err != nil {
			return err
		}
	}
	if strings.Contains(r.Kinds, "f") != (r.Dependencies != nil) {
		return invalid()
	}
	if r.Dependencies != nil {
		items := *r.Dependencies
		if items == nil {
			return invalid()
		}
		index := 0
		if err := visitDependencyDirections(selection, func(_ uint8, _ int, attrs []int16) error {
			if index < len(items) && slices.Equal(items[index].Attributes, attrs) {
				degree := items[index].Degree
				if math.IsNaN(degree) || math.IsInf(degree, 0) || degree <= 0 || degree > 1 {
					return invalid()
				}
				index++
			}
			return nil
		}); err != nil {
			return err
		}
		if index != len(items) {
			return invalid()
		}
	}
	return nil
}

// RestoreExtendedStatisticsSnapshots remints typed values under destination
// recipes and usages. No source identity grants destination authority.
func RestoreExtendedStatisticsSnapshots(ctx context.Context, read catalog.Read, records []ExtendedStatisticsSnapshot, codec StatisticsSnapshotCodec) (map[ids.ObjectAddress]*RelationMetadata, error) {
	var metadata map[ids.ObjectAddress]*RelationMetadata
	seen := make(map[ExtendedStatisticsKey]bool, len(records))
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
			d.Selection.Kinds() != r.Kinds || d.Selection.Len() != len(r.Keys) || d.Expressions.Len() != len(r.Expressions) || relation.Partition != nil && !r.Inherited {
			return invalid()
		}
		key := ExtendedStatisticsKey{Definition: read.Domain().Ref(read.Epoch(), d.Address()), Inherited: r.Inherited}
		if seen[key] {
			return invalid()
		}
		seen[key] = true
		fact := &ExtendedStatistics{Relation: d.Relation, Selection: d.Selection, Recipes: d.Expressions}
		fact.providers = CaptureStatisticsProviders(read, d)
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
		if len(fact.Items) != 0 {
			fact.Value, err = sqltype.StatisticsValue(typ, fact.Items)
			if err != nil {
				return nil, err
			}
		}
		if r.Dependencies != nil {
			dependencyType, ok := read.LookupType(sqltype.PgDependencies)
			if !ok {
				return invalid()
			}
			fact.Dependencies = sqltype.ResultNull(dependencyType.Usage())
			fact.DependencyItems = make([]sqltype.StatisticsItem, len(*r.Dependencies))
			for i, item := range *r.Dependencies {
				fact.DependencyItems[i] = sqltype.StatisticsItem{Attributes: slices.Clone(item.Attributes), Value: item.Degree}
			}
			if len(fact.DependencyItems) != 0 {
				fact.Dependencies, err = sqltype.StatisticsValue(dependencyType, fact.DependencyItems)
				if err != nil {
					return nil, err
				}
			}
		}
		if err := restoreExpressionSnapshots(ctx, read, relation, d, r.Expressions, fact, codec); err != nil {
			return nil, err
		}
		if err := restoreMCVSnapshot(ctx, read, r.MCV, fact, codec); err != nil {
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
