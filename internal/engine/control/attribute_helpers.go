package control

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

const AttributeHelperStem = "__stuntgres_attribute_history_"

type attributeEdge struct{ Child, Parent ids.ObjectAddress }

func AttributeHelperName(name string) bool {
	if !strings.HasPrefix(name, AttributeHelperStem) {
		return false
	}
	hash, collision, more := strings.Cut(name[len(AttributeHelperStem):], "_")
	n, err := strconv.ParseUint(hash, 16, 64)
	if err != nil || strconv.FormatUint(n, 16) != hash {
		return false
	}
	if more {
		n, err := strconv.ParseUint(collision, 10, 31)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != collision {
			return false
		}
	}
	return true
}

// Only scalar tombstones without any surviving possible parent provider need
// a helper. These descriptors are simulation records, never catalog injection:
// emitted ordinary SQL alone creates and removes the destination helper.
func (s *dumpScript) attributeModels(component []*catalog.Relation) ([]*catalog.Relation, map[ids.ObjectAddress]ids.ObjectAddress) {
	models := append([]*catalog.Relation(nil), component...)
	helpers := make(map[ids.ObjectAddress]ids.ObjectAddress)
	used := make(map[ids.NameKey]bool)
	for i, source := range component {
		var orphaned []int32
		for number, fact := range source.ColumnMeta.Dropped {
			if fact.Local || fact.Inheritance != 1 {
				continue
			}
			if _, possible := s.attributeHistory.origins[attributeSlot{source.Address(), number}]; !possible {
				orphaned = append(orphaned, number)
			}
		}
		if len(orphaned) == 0 {
			continue
		}
		slices.Sort(orphaned)
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(s.OriginalCatalog.Get(source.Namespace()).Name() + "." + source.Name()))
		base := AttributeHelperStem + strconv.FormatUint(hash.Sum64(), 16)
		name := base
		for n := 1; ; n++ {
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: source.Namespace(), Name: name}
			if s.OriginalCatalog.NameAddress(key) == (ids.ObjectAddress{}) && !used[key] {
				used[key] = true
				break
			}
			name = base + "_" + strconv.Itoa(n)
		}
		address := source.Address()
		address.SubID = -1
		helper := &catalog.Relation{ObjectAddress: address, ObjectNamespace: source.Namespace(), ObjectName: name, RelationKind: catalog.TableRelation, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(orphaned)), Dropped: make(map[int32]catalog.AttributeMetadata)}}
		helper.TableMethod = 2
		for j, number := range orphaned {
			n := int32(j + 1)
			helper.ColumnMeta.Dropped[n] = catalog.AttributeMetadata{Number: n, Dropped: true, Local: true}
			s.attributeHistory.names[attributeSlot{address, n}] = s.attributeHistory.names[attributeSlot{source.Address(), number}]
		}
		copySource := *source
		graph := catalog.TableInheritance{Columns: make(map[int32]catalog.InheritedColumn)}
		if source.Inheritance != nil {
			graph = *source.Inheritance
			graph.Parents = append([]catalog.InheritanceParent(nil), source.Inheritance.Parents...)
		}
		if source.Inheritance == nil {
			for _, number := range source.Attributes {
				graph.Columns[number] = catalog.InheritedColumn{Local: true}
			}
		}
		graph.Parents = append(graph.Parents, catalog.InheritanceParent{Relation: address, Seqno: int32(len(graph.Parents) + 1)})
		copySource.Inheritance = &graph
		models[i] = &copySource
		models = append(models, helper)
		helpers[address] = source.Address()
	}
	return models, helpers
}

func (s *dumpScript) emitHelperOperation(ctx context.Context, w *dumpWriter, plan *attributeSchedule, op attributeOperation, event dump.Event) error {
	model := plan.models[op.Relation]
	qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(model.Namespace()).Name()) + "." + catalog.QuoteIdentifier(model.Name())
	switch op.Kind {
	case 'c':
		return w.emit("CREATE TABLE " + qualified + " ();\n")
	case 'n':
		parent := plan.models[op.Helper]
		return w.emit("ALTER TABLE " + qualified + " NO INHERIT " + catalog.QuoteIdentifier(s.OriginalCatalog.Get(parent.Namespace()).Name()) + "." + catalog.QuoteIdentifier(parent.Name()) + ";\n")
	case 'x':
		return w.emit("DROP TABLE " + qualified + ";\n")
	case 'a':
		return s.emitBound(w, castConsumer{Event: event}, "ALTER TABLE "+qualified+" ADD COLUMN "+catalog.QuoteIdentifier(s.attributeHistory.names[attributeSlot{op.Relation, op.Number}])+" pg_catalog.int4;\n")
	}
	return fmt.Errorf("stuntgres: invalid attribute helper operation")
}
