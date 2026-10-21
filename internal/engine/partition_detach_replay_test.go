package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestPartitionDetachReplayEvents(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "real_backing_after_detach", true: "childless_collision_reserved"}[empty], func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			f.child.SQLIndex.PartitionParent = f.parent.Address()
			if empty {
				f.root.Partition.Children = nil
				f.root.Partition.DefaultChild = ids.ObjectAddress{}
				f.leaf.Membership = nil
				f.child.SQLIndex.PartitionParent = ids.ObjectAddress{}
			}
			reserved := ids.NameKey{ClassID: ids.RelationClassID, Namespace: f.root.Namespace(), Name: partitionReplayStem + "1"}
			f.view.State().BindName(reserved, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46700})
			r, c, pi, ci := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID
			graph := dump.Graph{{OID: r}: nil, {OID: c}: nil, {OID: pi}: {{OID: r}}, {OID: ci}: {{OID: c}}}
			if !empty {
				graph[dump.Event{OID: c}] = []dump.Event{{OID: r}}
			}
			declarations := []dump.Event{{OID: r}, {OID: c}, {OID: pi}, {OID: ci}}
			tables := []dumpRelation{{descriptor: f.root}, {descriptor: f.leaf}}
			indices := map[ids.OID]int{pi: 0, ci: 1}
			replay := &partitionConstraintReplay{original: f.view, temporary: map[ids.ObjectAddress]string{}}
			if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			plans := partitionInvalidDumpEvents(f.view, graph, indices)
			order, err := dump.Order(context.Background(), graph, nil)
			if err != nil {
				t.Fatal("replay introduced cycle", err)
			}
			position := func(event dump.Event) int {
				for i, candidate := range order {
					if candidate == event {
						return i
					}
				}
				t.Fatal("missing event", event)
				return -1
			}
			prepare := dump.Event{OID: r, Phase: dump.InvalidIndexPreparation}
			finish := dump.Event{OID: r, Phase: dump.InvalidIndexCompletion}
			if position(prepare) >= position(finish) || position(finish) >= position(dump.Event{OID: r, Phase: dump.TableCompletion}) {
				t.Fatal("teardown/reattach not a completion predecessor", order)
			}
			if empty {
				if plans[r].scratch != partitionReplayStem+"2" || position(prepare) >= position(dump.Event{OID: pi}) {
					t.Fatal("scratch collision/declaration ordering", plans[r], order)
				}
				text, err := plans[r].sql(context.Background(), f.view, dump.InvalidIndexCompletion)
				if err != nil || !strings.Contains(text, "DETACH PARTITION") || !strings.Contains(text, "DROP TABLE") {
					t.Fatal("missing real teardown", text, err)
				}
			} else {
				if plans[r].child != f.leaf || position(prepare) >= position(dump.Event{OID: ci}) || position(dump.Event{OID: ci}) >= position(finish) {
					t.Fatal("real backing must build standalone between detach and table reattach", order)
				}
				if _, exists := graph[dump.Event{OID: ci, Phase: dump.IndexAttachment}]; exists {
					t.Fatal("explicit completion would promote preserved invalid parent")
				}
				text, err := plans[r].sql(context.Background(), f.view, dump.InvalidIndexCompletion)
				if err != nil || !strings.Contains(text, "ATTACH PARTITION") || !strings.Contains(text, "DEFAULT") {
					t.Fatal("lost captured bound", text, err)
				}
			}
			if f.parent.SQLIndex.Valid || f.child.SQLIndex.PartitionParent != (map[bool]ids.ObjectAddress{false: f.parent.Address(), true: {}}[empty]) {
				t.Fatal("renderer mutated captured definitions")
			}
		})
	}
}

func TestPartitionDetachReplayLocalEvents(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	f.parent.SQLIndex.Include = nil
	f.child.SQLIndex.Include = nil
	for _, index := range [2]*catalog.Relation{f.parent, f.child} {
		index.Columns = index.Columns[:2]
		index.Attributes = index.Attributes[:2]
		index.ColumnMeta.HighWater = 2
		index.Index = map[string]int{"a": 0, "b": 1}
		index.SQLIndex.Keys[1].Desc = false
		index.SQLIndex.Keys[1].NullsFirst = false
	}
	local, definition := *f.child, *f.child.SQLIndex
	local.ObjectAddress.ObjectID = 19999
	local.ObjectName = "unit_local_older"
	definition.PartitionParent = ids.ObjectAddress{}
	constraint := catalog.UniqueConstraint{OID: 46900, Name: local.Name(), Index: local.Address(), Attributes: []int32{22, 11}, IsLocal: true}
	definition.Constraint = UniqueBacking(&constraint)
	local.SQLIndex = &definition
	f.leaf.Constraints.Unique = []catalog.UniqueConstraint{constraint}
	f.view.State().SetDescriptor(local.Address(), &local)
	f.view.State().BindName(catalog.DescriptorNameKey(&local), local.Address())
	if !PartitionReplayUsingIndex(f.view, &local) || UsingIndexDump(&local) {
		t.Fatal("local origin must be explicit only for this captured replay")
	}
	r, c, pi, ci, li := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID, local.Address().ObjectID
	regclass := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).SQLType
	consumer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46901}, ObjectNamespace: f.leaf.Namespace(), Owner: f.leaf.Owner, ObjectName: "unit_local_consumer", ObjectRevision: 1, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "ref", Usage: regclass.Usage()}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"ref": 0}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.Address().ObjectID), 10))
	consumer.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 46902, Expression: plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: value}, Dependencies: []ids.ObjectRef{f.e.catalog.Domain.Ref(f.view.Epoch(), local.Address())}}}
	f.view.State().SetDescriptor(consumer.Address(), consumer)
	f.view.State().BindName(catalog.DescriptorNameKey(consumer), consumer.Address())
	provider, err := dumpPredecessor(f.view, consumer.ColumnMeta.Defaults[1].Dependencies[0].Address, consumer.Address())
	if err != nil {
		t.Fatal(err)
	}
	graph := dump.Graph{{OID: r}: nil, {OID: c}: {{OID: r}}, {OID: pi}: {{OID: r}}, {OID: ci}: {{OID: c}}, {OID: li}: {{OID: c}}, {OID: consumer.Address().ObjectID}: {{OID: provider}}}
	declarations, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A real captured private-name projection exercises the local constraint's
	// rename barrier. Stored REGCLASS data must wait for that original identity.
	tables := []dumpRelation{{descriptor: f.root}, {descriptor: f.leaf}, {descriptor: consumer, columns: consumer.Columns, attributes: consumer.Attributes, defaults: consumer.ColumnMeta.Defaults, versions: []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{value}}}}}
	indices := map[ids.OID]int{pi: 0, ci: 1, li: 2}
	_, replay := partitionConstraintDumpCatalog(f.view)
	if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
		t.Fatal(err)
	}
	partitionInvalidDumpEvents(f.view, graph, indices)
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal("deferred local origin created cycle", err)
	}
	position := func(event dump.Event) int {
		for i, candidate := range order {
			if candidate == event {
				return i
			}
		}
		t.Fatal("missing event", event)
		return -1
	}
	prepare := dump.Event{OID: r, Phase: dump.InvalidIndexPreparation}
	finish := dump.Event{OID: r, Phase: dump.InvalidIndexCompletion}
	if position(prepare) >= position(dump.Event{OID: ci}) || position(dump.Event{OID: ci}) >= position(finish) || position(finish) >= position(dump.Event{OID: li}) {
		t.Fatal("older local index can steal captured backing", order)
	}
	rename := dump.Event{OID: li, Phase: dump.Rename}
	rows := dump.Event{OID: consumer.Address().ObjectID, Phase: dump.Rows}
	if position(dump.Event{OID: consumer.Address().ObjectID}) >= position(rename) {
		t.Fatal("private-name default declaration lost its backing before binding", order)
	}
	if position(dump.Event{OID: li}) >= position(rename) || position(rename) >= position(rows) {
		t.Fatal("consumer replay precedes its own local index identity", order)
	}
	if f.leaf.Constraints.Unique[0].OID != constraint.OID || local.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.child.SQLIndex.PartitionParent != f.parent.Address() {
		t.Fatal("scheduler changed source ownership")
	}
}
