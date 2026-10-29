package partitioncmds_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
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
			reserved := ids.NameKey{ClassID: ids.RelationClassID, Namespace: f.root.Namespace(), Name: partition.ReplayStem + "1"}
			f.view.State().BindName(reserved, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46700})
			r, c, pi, ci := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID
			graph := dump.Graph{{OID: r}: nil, {OID: c}: nil, {OID: pi}: {{OID: r}}, {OID: ci}: {{OID: c}}}
			if !empty {
				graph[dump.Event{OID: c}] = []dump.Event{{OID: r}}
			}
			declarations := []dump.Event{{OID: r}, {OID: c}, {OID: pi}, {OID: ci}}
			tables := []table.DumpRelation{{Descriptor: f.root}, {Descriptor: f.leaf}}
			indices := map[ids.OID]int{pi: 0, ci: 1}
			replay := &partition.ConstraintReplay{Original: f.view, Temporary: map[ids.ObjectAddress]string{}}
			if err := partition.DumpEvents(context.Background(), control.Resolver{}, f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			plans, err := partition.InvalidDumpEvents(control.Resolver{}, f.view, graph, indices, tables)
			if err != nil {
				t.Fatal(err)
			}
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
				if plans[r].Scratch != partition.ReplayStem+"2" || position(prepare) >= position(dump.Event{OID: pi}) {
					t.Fatal("scratch collision/declaration ordering", plans[r], order)
				}
				text, err := plans[r].SQL(context.Background(), f.view, dump.InvalidIndexCompletion)
				if err != nil || !strings.Contains(text, "DETACH PARTITION") || !strings.Contains(text, "DROP TABLE") {
					t.Fatal("missing real teardown", text, err)
				}
			} else {
				if plans[r].Child != f.leaf || position(prepare) >= position(dump.Event{OID: ci}) || position(dump.Event{OID: ci}) >= position(finish) {
					t.Fatal("real backing must build standalone between detach and table reattach", order)
				}
				if _, exists := graph[dump.Event{OID: ci, Phase: dump.IndexAttachment}]; exists {
					t.Fatal("explicit completion would promote preserved invalid parent")
				}
				text, err := plans[r].SQL(context.Background(), f.view, dump.InvalidIndexCompletion)
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
	definition.Constraint = index.UniqueBacking(&constraint)
	local.SQLIndex = &definition
	f.leaf.Constraints.Unique = []catalog.UniqueConstraint{constraint}
	f.view.State().SetDescriptor(local.Address(), &local)
	f.view.State().BindName(catalog.DescriptorNameKey(&local), local.Address())
	if !index.PartitionReplayUsingIndex(f.view, &local) || index.UsingIndexDump(&local) {
		t.Fatal("local origin must be explicit only for this captured replay")
	}
	r, c, pi, ci, li := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID, local.Address().ObjectID
	regclass := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).SQLType
	consumer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46901}, ObjectNamespace: f.leaf.Namespace(), Owner: f.leaf.Owner, ObjectName: "unit_local_consumer", ObjectRevision: 1, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "ref", Usage: regclass.Usage()}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"ref": 0}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.Address().ObjectID), 10))
	consumer.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 46902, Expression: plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: value}, Dependencies: []ids.ObjectRef{f.e.Catalog.Domain.Ref(f.view.Epoch(), local.Address())}}}
	f.view.State().SetDescriptor(consumer.Address(), consumer)
	f.view.State().BindName(catalog.DescriptorNameKey(consumer), consumer.Address())
	provider, err := control.Resolver{}.Predecessor(f.view, consumer.ColumnMeta.Defaults[1].Dependencies[0].Address, consumer.Address())
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
	tables := []table.DumpRelation{{Descriptor: f.root}, {Descriptor: f.leaf}, {Descriptor: consumer, Columns: consumer.Columns, Attributes: consumer.Attributes, Defaults: consumer.ColumnMeta.Defaults, Versions: []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{value}}}}}
	indices := map[ids.OID]int{pi: 0, ci: 1, li: 2}
	_, replay := partition.ConstraintDumpCatalog(control.Resolver{}, f.view)
	if err := partition.DumpEvents(context.Background(), control.Resolver{}, f.view, graph, declarations, tables, indices, replay, false); err != nil {
		t.Fatal(err)
	}
	if _, err := partition.InvalidDumpEvents(control.Resolver{}, f.view, graph, indices, tables); err != nil {
		t.Fatal(err)
	}
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

func partitionConstraintDescriptors(t *testing.T) partitionConstraintUnit {
	t.Helper()
	root, idx, catalogSnapshot, types := indexClausesFixture(t)
	e := engine.New()
	state := *e.Catalog.State
	state.ResetMaps()
	for a, d := range e.Catalog.State.Descriptors() {
		state.SetDescriptor(a, d)
	}
	for k, a := range e.Catalog.State.Names() {
		state.BindName(k, a)
	}
	root.ObjectName = "unit_root"
	root.RelationKind = catalog.PartitionedRelation
	keyClass, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	comparison, err := routine.BindIndexOrdering(e.Catalog.Read(&state), root.Columns[0].Usage, keyClass)
	if err != nil {
		t.Fatal(err)
	}
	root.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 10, Usage: root.Columns[0].Usage, Class: keyClass, Comparison: comparison, Label: "a"}}}
	idx.ObjectName = "unit_parent"
	idx.SQLIndex.Unique = true
	idx.SQLIndex.Valid = false
	leaf := *root
	leaf.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	leaf.ObjectName = "unit_leaf"
	leaf.Partition = nil
	leaf.RelationKind = catalog.TableRelation
	leaf.TableMethod = 2
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), DefaultBound: true}
	leaf.Columns = []catalog.Column{root.Columns[1], root.Columns[0], root.Columns[2]}
	leaf.Attributes = []int32{11, 22, 33}
	leaf.ColumnMeta.HighWater = 33
	leaf.Index = map[string]int{"b": 0, "a": 1, "p": 2}
	root.Partition.Children = []ids.ObjectAddress{leaf.Address()}
	root.Partition.DefaultChild = leaf.Address()
	for a, d := range catalogSnapshot.Descriptors() {
		// The reduced fixture uses a different engine's immutable seed.
		// Retain this engine's genuine callable and principal authority.
		if state.Get(a) == nil {
			state.SetDescriptor(a, d)
		}
	}
	state.SetDescriptor(leaf.Address(), &leaf)
	view := e.Catalog.Read(&state)
	mapped, err := index.MapPartitionIndex(context.Background(), view, root, &leaf, idx.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	mapped.Valid = true
	child := *idx
	child.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}
	child.ObjectName = "unit_child"
	child.SQLIndex = mapped
	child.Columns = slices.Clone(idx.Columns)
	child.Attributes = slices.Clone(idx.Attributes)
	state.SetDescriptor(child.Address(), &child)
	for _, d := range []*catalog.Relation{root, &leaf, idx, &child} {
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.Catalog.PublishLocked(&state)
	return partitionConstraintUnit{e: e, root: root, leaf: &leaf, parent: idx, child: &child, view: view}
}

type partitionConstraintUnit struct {
	e                         *core.Engine
	root, leaf, parent, child *catalog.Relation
	view                      catalog.Read
}

func indexClausesFixture(t *testing.T) (*catalog.Relation, *catalog.Relation, *catalog.State, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	types := indexClausesTypes()
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation, Attributes: []int32{10, 20, 30}, ColumnMeta: catalog.ColumnMetadata{HighWater: 30}, Columns: []catalog.Column{{Name: "a", Usage: types[sqltype.Int4].Usage()}, {Name: "b", Usage: types[sqltype.Int4].Usage()}, {Name: "p", Usage: types[sqltype.Text].Usage()}}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	c, ok, defaultErr := operatorFamilyTestRead().DefaultIndexClass(403, types[sqltype.Int4])
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if !ok {
		t.Fatal("missing current integer default class")
	}
	def := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 10, Class: c}, {Attribute: 20, Class: c, Desc: true, NullsFirst: true}}, Include: []int32{30}, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, ObjectNamespace: parent.Namespace(), ObjectName: "cover", ObjectRevision: 1, RelationKind: catalog.IndexRelation, SQLIndex: def, Columns: append([]catalog.Column(nil), parent.Columns...), Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0, "b": 1, "p": 2}}
	catalogSnapshot := operatorFamilyFixtureState(catalogStateFixture(0, map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, idx.Address(): idx}, nil))
	if err := index.ValidateIndexDefinition(catalog.NewRead(nil, catalogSnapshot), idx); err != nil {
		t.Fatal(err)
	}
	return parent, idx, catalogSnapshot, types
}

func indexClausesTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func operatorFamilyFixtureState(state *catalog.State) *catalog.State {
	for address, descriptor := range operatorFamilyTestRead().Descriptors() {
		switch address.ClassID {
		case ids.AccessMethodClassID, ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.ProcedureClassID, ids.OperatorClassID:
			state.SetDescriptor(address, descriptor)
			state.BindName(catalog.DescriptorNameKey(descriptor), address)
		}
	}
	return state
}

// catalogStateFixture builds an unpublished state at epoch that holds
// descriptors and names, through the state's write API.
func catalogStateFixture(epoch ids.CatalogEpoch, descriptors map[ids.ObjectAddress]catalog.Descriptor, names map[ids.NameKey]ids.ObjectAddress) *catalog.State {
	s := catalog.NewState(epoch)
	for a, d := range descriptors {
		s.SetDescriptor(a, d)
	}
	for k, a := range names {
		s.BindName(k, a)
	}
	return s
}
