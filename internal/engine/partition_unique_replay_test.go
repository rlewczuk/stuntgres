package engine

import (
	"context"
	"slices"
	"strconv"
	"stuntgres/internal/engine/control"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionUniqueReplayUnit(t *testing.T, primary bool) (*core.Engine, *catalog.Relation, *catalog.Relation, *parser.AlterTable) {
	t.Helper()
	e := New().RestoreStage()
	root := partitionAttachUnitRoot(t, e, "unique_replay_root", 47200)
	mid := partitionAttachUnitRoot(t, e, "unique_replay_mid", 47201)
	leaf := inheritanceUnitRelation(e, "unique_replay_leaf", 47202, "k")
	leaf.RelationKind = catalog.TableRelation
	e.Mu.Lock()
	for _, tbl := range [3]*catalog.Relation{root, mid, leaf} {
		tbl.Columns[0].Nullable = false
		tbl.Constraints.NotNull = []catalog.NotNullConstraint{{Name: tbl.Name() + "_nn", Attribute: 1}}
		tbl.History.HasIndexes = true
		// Stored NOT NULL facts need real catalog identities before validation,
		// just as the canonical CREATE producer assigns them.
		if err := table.AssignDefinitionOIDs(core.DDLHost{E: e}, tbl); err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
	}
	e.Mu.Unlock()
	root.Partition.Children = []ids.ObjectAddress{mid.Address()}
	root.Partition.DefaultChild = mid.Address()
	mid.Membership = &catalog.PartitionMembership{Parent: root.Address(), DefaultBound: true}
	mid.Partition.Children = []ids.ObjectAddress{leaf.Address()}
	mid.Partition.DefaultChild = leaf.Address()
	leaf.Membership = &catalog.PartitionMembership{Parent: mid.Address(), DefaultBound: true}
	class, _ := sqltype.DefaultIndexClass(mid.Columns[0].Usage.Type)
	index := func(table *catalog.Relation, id ids.OID, name string, parent ids.ObjectAddress, valid bool) *catalog.Relation {
		columns := slices.Clone(table.Columns)
		columns[0].OriginTable = uint32(id)
		columns[0].OriginAttribute = 1
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: table.Namespace(), Owner: table.Owner, ObjectName: name, ObjectRevision: 1, RelationKind: catalog.IndexRelation, Columns: columns, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}, SQLIndex: &catalog.IndexDefinition{Parent: table.Address(), PartitionParent: parent, Method: catalog.BtreeMethod, MethodOID: 403, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Unique: true, Valid: valid, Ready: true, Live: true}}
	}
	ri := index(root, 47203, "unique_replay_root_plain", ids.ObjectAddress{}, false)
	mi := index(mid, 47204, "unique_replay_mid_plain", ri.Address(), true)
	li := index(leaf, 47205, "unique_replay_leaf_plain", mi.Address(), true)
	partitionAttachUnitInstall(t, e, root, mid, leaf, ri, mi, li)
	mid = e.Catalog.State.MustRelation(mid.Address())
	leaf = e.Catalog.State.MustRelation(leaf.Address())
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: mid.Name()}}}, Actions: []parser.AlterTableAction{{Action: parser.AddUnique, Unique: &parser.UniqueConstraint{Name: parser.Identifier{Text: "__stuntgres_partition_key_1"}, Columns: []parser.Identifier{{Text: "k"}}, Primary: primary}}}}
	return e, mid, leaf, raw
}

func TestPartitionDetachUniqueReplayAuthority(t *testing.T) {
	e, mid, leaf, raw := partitionUniqueReplayUnit(t, true)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	if err := BeginPartitionUniqueReplay(e, op, raw); err != nil {
		t.Fatal(err)
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if !core.PartitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("exact origin missing")
	}
	copied := *op
	if core.PartitionUniqueReplayAllowed(&copied, auth, raw, mid) {
		t.Fatal("copied lease authorized")
	}
	lookalike := *mid
	if core.PartitionUniqueReplayAllowed(op, auth, raw, &lookalike) {
		t.Fatal("copied table authorized")
	}
	raw.Actions[0].Unique.Columns[0].Text = "other"
	if core.PartitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("changed definition authorized")
	}
	raw.Actions[0].Unique.Columns[0].Text = "k"
	changed := *leaf
	changed.ObjectRevision++
	e.Catalog.State.SetDescriptor(leaf.Address(), &changed)
	if core.PartitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("changed subtree authorized")
	}
	e.Catalog.State.SetDescriptor(leaf.Address(), leaf)
	core.OperationReplay(op).PartitionUnique = nil
	if partition.ConsumerBoundary(raw, mid, core.PartitionReplayAllowed(op, auth)) == nil {
		t.Fatal("public hierarchy ADD widened")
	}
}

func TestPartitionDetachUniqueReplayPublication(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, cancel := range []bool{false, true} {
			t.Run(map[bool]string{false: "unique", true: "primary"}[primary]+map[bool]string{false: "/publish", true: "/cancel"}[cancel], func(t *testing.T) {
				e, mid, leaf, raw := partitionUniqueReplayUnit(t, primary)
				scope, op := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
				if err := BeginPartitionUniqueReplay(e, op, raw); err != nil {
					t.Fatal(err)
				}
				if err := scope.AcquireRelation(raw, true); err != nil {
					t.Fatal(err)
				}
				before := e.Catalog.State
				e.Mu.Lock()
				auth, err := e.StatementAuthorizationLocked(op, raw)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				_, err = tablecmds.AlterTableLocked(e, scope, op, auth, raw, 0)
				if err != nil {
					t.Fatal(err)
				}
				if e.Catalog.State != before {
					t.Fatal("private staging published before Finish")
				}
				if cancel {
					op.Cancel(context.Canceled)
				}
				err = scope.Finish(nil)
				op.Done()
				if cancel {
					if err == nil || e.Catalog.State != before {
						t.Fatal("canceled origin published", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				current := e.Catalog.State.MustRelation(mid.Address())
				child := e.Catalog.State.MustRelation(leaf.Address())
				if len(current.Constraints.Unique) != 1 || len(child.Constraints.Unique) != 1 || child.Constraints.Unique[0].Parent != current.Constraints.Unique[0].OID {
					t.Fatal("missing real constraint ancestry")
				}
				rootIndex := e.Catalog.State.MustRelation(current.Constraints.Unique[0].Index)
				childIndex := e.Catalog.State.MustRelation(child.Constraints.Unique[0].Index)
				if !rootIndex.SQLIndex.Valid || childIndex.SQLIndex.PartitionParent != rootIndex.Address() || childIndex.SQLIndex.Constraint.Index != child.Constraints.Unique[0].Index || childIndex.SQLIndex.Constraint.Primary != primary {
					t.Fatal("wrong local backing tree")
				}
				if e.Catalog.State.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47204}) != before.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47204}) || e.Catalog.State.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47205}) != before.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47205}) {
					t.Fatal("attached plain tree consumed")
				}
				if len(child.Constraints.NotNull) != len(leaf.Constraints.NotNull) || len(current.Constraints.NotNull) != len(mid.Constraints.NotNull) {
					t.Fatal("private PK added NN rollout")
				}
			})
		}
	}
}

func TestPartitionDetachUniqueReplayEvents(t *testing.T) {
	e, mid, leaf, raw := partitionUniqueReplayUnit(t, true)
	scope, op := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
	if err := BeginPartitionUniqueReplay(e, op, raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	if _, err = tablecmds.AlterTableLocked(e, scope, op, auth, raw, 0); err != nil {
		t.Fatal(err)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	view := e.CatalogRead()
	mid = view.MustRelation(mid.Address())
	leaf = view.MustRelation(leaf.Address())
	local := view.MustRelation(mid.Constraints.Unique[0].Index)
	child := view.MustRelation(leaf.Constraints.Unique[0].Index)
	if index.PartitionReplayUsingIndex(view, local) || index.PartitionReplayVirtualUniqueRoot(view, child) != local {
		t.Fatal("virtual definition assigned a physical origin")
	}
	provider, err := control.Resolver{}.Predecessor(view, child.Address(), ids.ObjectAddress{})
	if err != nil || provider != local.Address().ObjectID {
		t.Fatal("subordinate provider is not the real ADD", provider, err)
	}
	graph := make(dump.Graph)
	var tables []table.DumpRelation
	indices := make(map[ids.OID]int)
	for _, descriptor := range view.Descriptors() {
		r, ok := descriptor.(*catalog.Relation)
		if !ok || r.Address().ObjectID < 47200 || r.Address().ObjectID > 47205 {
			continue
		}
		if r.SQLIndex == nil {
			graph[dump.Event{OID: r.Address().ObjectID}] = nil
			if r.Membership != nil {
				graph[dump.Event{OID: r.Address().ObjectID}] = []dump.Event{{OID: r.Membership.Parent.ObjectID}}
			}
			tables = append(tables, table.DumpRelation{Descriptor: r})
		} else {
			graph[dump.Event{OID: r.Address().ObjectID}] = []dump.Event{{OID: r.SQLIndex.Parent.ObjectID}}
			indices[r.Address().ObjectID] = len(indices)
		}
	}
	graph[dump.Event{OID: local.Address().ObjectID}] = []dump.Event{{OID: mid.Address().ObjectID}}
	indices[local.Address().ObjectID] = len(indices)
	regclass := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).SQLType
	consumer := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47206}, ObjectNamespace: mid.Namespace(), Owner: mid.Owner, ObjectName: "unique_replay_consumer", ObjectRevision: 1, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "ref", Usage: regclass.Usage()}, {Name: "leaf_ref", Usage: regclass.Usage()}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Index: map[string]int{"ref": 0, "leaf_ref": 1}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.Address().ObjectID), 10))
	leafValue := sqltype.String(regclass, strconv.FormatUint(uint64(child.Address().ObjectID), 10))
	state := *view.State()
	state.ResetDescriptors()
	for address, descriptor := range view.Descriptors() {
		state.SetDescriptor(address, descriptor)
	}
	state.SetDescriptor(consumer.Address(), consumer)
	view = view.WithState(&state)
	graph[dump.Event{OID: consumer.Address().ObjectID}] = []dump.Event{{OID: local.Address().ObjectID}}
	tables = append(tables, table.DumpRelation{Descriptor: consumer, Columns: consumer.Columns, Attributes: consumer.Attributes, Versions: []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{value, leafValue}}}})
	declarations, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, replay := partition.ConstraintDumpCatalog(control.Resolver{}, view)
	if err = partition.DumpEvents(context.Background(), control.Resolver{}, view, graph, declarations, tables, indices, replay, false); err != nil {
		t.Fatal(err)
	}
	partition.InvalidDumpEvents(view, graph, indices)
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil {
		t.Fatal("virtual definition closes initial membership cycle", err)
	}
	before := func(a, b dump.Event) {
		t.Helper()
		i, j := slices.Index(order, a), slices.Index(order, b)
		if i < 0 || j < 0 || i >= j {
			t.Fatal("wrong real definition/name lifetime", a, b, order)
		}
	}
	before(dump.Event{OID: 47200, Phase: dump.InvalidIndexCompletion}, dump.Event{OID: local.Address().ObjectID})
	before(dump.Event{OID: local.Address().ObjectID}, dump.Event{OID: child.Address().ObjectID, Phase: dump.Rename})
	before(dump.Event{OID: consumer.Address().ObjectID}, dump.Event{OID: local.Address().ObjectID, Phase: dump.Rename})
	before(dump.Event{OID: local.Address().ObjectID, Phase: dump.Rename}, dump.Event{OID: consumer.Address().ObjectID, Phase: dump.Rows})
	before(dump.Event{OID: child.Address().ObjectID, Phase: dump.Rename}, dump.Event{OID: consumer.Address().ObjectID, Phase: dump.Rows})
	if len(mid.Constraints.NotNull) != 1 || len(leaf.Constraints.NotNull) != 1 {
		t.Fatal("graph erased NN provenance")
	}
}
