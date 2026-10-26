package control

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func attributeUnitRelation(id ids.OID, name string, high int32) *catalog.Relation {
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectName: name, RelationKind: catalog.TableRelation, Index: make(map[string]int), ColumnMeta: catalog.ColumnMetadata{HighWater: high, Dropped: make(map[int32]catalog.AttributeMetadata)}}
}
func attributeUnitLive(r *catalog.Relation, number int32, name string, local bool, parent *catalog.Relation, parentNumber int32) {
	r.Index[name] = len(r.Columns)
	r.Columns = append(r.Columns, catalog.Column{Name: name})
	r.Attributes = append(r.Attributes, number)
	if r.Inheritance != nil {
		p := catalog.InheritedColumn{Local: local}
		if parent != nil {
			p.Origins = []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: parentNumber}}
		}
		r.Inheritance.Columns[number] = p
	}
}
func attributeUnitParent(child, parent *catalog.Relation) {
	child.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: parent.Address(), Seqno: 1}}, Columns: make(map[int32]catalog.InheritedColumn)}
}
func attributeUnitHole(r *catalog.Relation, number int32, local bool, count int64) {
	r.ColumnMeta.Dropped[number] = catalog.AttributeMetadata{Number: number, Dropped: true, Local: local, Inheritance: count}
}

func TestAccessRelationsAttributeSchedule(t *testing.T) {
	for _, kind := range []string{"mixed", "different_numbers", "interleaved"} {
		t.Run(kind, func(t *testing.T) {
			fixture := func() []*catalog.Relation {
				p := attributeUnitRelation(41000, "p", 1)
				c := attributeUnitRelation(41001, "c", 3)
				attributeUnitParent(c, p)
				switch kind {
				case "mixed":
					attributeUnitLive(p, 1, "b", true, nil, 0)
					attributeUnitLive(c, 1, "a", true, nil, 0)
					attributeUnitLive(c, 2, "b", false, p, 1)
					attributeUnitHole(c, 3, true, 0)
					return []*catalog.Relation{c, p}
				case "different_numbers":
					p.ColumnMeta.HighWater = 2
					attributeUnitLive(p, 1, "p", true, nil, 0)
					attributeUnitHole(p, 2, true, 0)
					attributeUnitLive(c, 1, "p", false, p, 1)
					attributeUnitLive(c, 2, "local", true, nil, 0)
					attributeUnitHole(c, 3, false, 1)
					return []*catalog.Relation{c, p}
				default:
					p.ColumnMeta.HighWater = 2
					c.ColumnMeta.HighWater = 2
					attributeUnitHole(p, 1, true, 0)
					attributeUnitHole(p, 2, true, 0)
					attributeUnitHole(c, 1, false, 1)
					attributeUnitHole(c, 2, false, 1)
					d := attributeUnitRelation(41002, "d", 1)
					attributeUnitParent(d, p)
					attributeUnitHole(d, 1, false, 1)
					return []*catalog.Relation{c, d, p}
				}
			}
			relations := fixture()
			result, err := scheduleAttributes(context.Background(), relations, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(relations, fixture()) {
				t.Fatal("scheduler mutated captured descriptors")
			}
			again, err := scheduleAttributes(context.Background(), relations, nil)
			if err != nil || !reflect.DeepEqual(result, again) {
				t.Fatal("nondeterministic construction", err)
			}
			p := relations[len(relations)-1]
			c := relations[0]
			find := func(kind byte, address ids.ObjectAddress, number int32) int {
				for i, op := range result.Operations {
					if op.Kind == kind && op.Relation == address && (number == 0 || op.Number == number) {
						return i
					}
				}
				return -1
			}
			if kind == "mixed" {
				if a, b := find('a', c.Address(), 1), find('a', p.Address(), 1); a < 0 || b <= a {
					t.Fatal("inherited merge consumed captured local slot", result.Operations)
				}
				if attrs := result.Relations[c.Address()].Attributes; len(attrs) != 3 || !attrs[0].Local || attrs[1].Local || !attrs[2].Dropped {
					t.Fatal("mixed identity", attrs)
				}
			} else if kind == "different_numbers" {
				if result.Groups[attributeSlot{c.Address(), 3}] != (attributeSlot{p.Address(), 2}) {
					t.Fatal("different numeric origin was not reconstructed", result.Groups)
				}
			} else {
				d := relations[1]
				first, created, last := find('d', p.Address(), 1), find('c', d.Address(), 0), find('d', p.Address(), 2)
				if first < 0 || created <= first || last <= created {
					t.Fatal("constructor/drop frontier was batched", result.Operations)
				}
				for _, r := range relations {
					if len(result.Relations[r.Address()].Attributes) != int(r.ColumnMeta.HighWater) {
						t.Fatal("fabricated physical highwater", r.Name())
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := scheduleAttributes(ctx, relations, nil); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation", err)
			}
		})
	}
}
