package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func materializedAlterUnit(t *testing.T, e *Engine, op *Operation, d *relationDescriptor, action, target string) error {
	t.Helper()
	return extensionUnitExecute(t, e, op, &parser.AlterView{Materialized: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, Action: action, Target: parser.Identifier{Text: target}})
}
func materializedAlterIndex(t *testing.T, e *Engine, op *Operation, d *relationDescriptor) *relationDescriptor {
	t.Helper()
	// Setup uses the real index binder and publisher; assertions below inspect roots.
	roleExecute(t, e, op, "CREATE UNIQUE INDEX unit_mv_i ON "+d.name+"(i)")
	return e.state.descriptors[e.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.namespace, Name: "unit_mv_i"}]].(*relationDescriptor)
}
func TestMaterializedAlterIdentity(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	store := e.storage[d.address]
	rule, rowType := d.view.rule, d.rowType
	if err := materializedAlterUnit(t, e, op, d, "rename", "saved"); err != nil {
		t.Fatal(err)
	}
	next := e.state.descriptors[d.address].(*relationDescriptor)
	if next == d || next.name != "saved" || d.name != "unit_mv" || next.rowType != rowType || next.view.rule != rule || next.view != d.view || e.storage[d.address] != store || !reflect.DeepEqual(next.attributes, d.attributes) {
		t.Fatal("metadata publication reminted or mutated captured identity")
	}
	row := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rowType)}].(*typeDescriptor)
	if row.Name() != "saved" || row.owningRelation != d.address {
		t.Fatal("row type rename not synchronized")
	}
}
func TestMaterializedAlterOwnerAttachments(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	index := materializedAlterIndex(t, e, op, d)
	root := e.indexRoots[index.address]
	store := e.storage[d.address]
	actor := roleAttributeCreate(t, e, op, "mv_next", true, parser.RoleOptions{})
	oldRow := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.rowType)}].(*typeDescriptor)
	oldArray := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oldRow.array)}].(*typeDescriptor)
	rowACL, arrayACL := oldRow.acl, oldArray.acl
	rowACL.Entries = slices.Clone(rowACL.Entries)
	arrayACL.Entries = slices.Clone(arrayACL.Entries)
	stmt := &parser.AlterView{Materialized: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.name}}}, Action: "owner", Target: parser.Identifier{Text: "mv_next"}}
	if err := extensionUnitExecute(t, e, op, stmt); err != nil {
		t.Fatal(err)
	}
	next := e.state.descriptors[d.address].(*relationDescriptor)
	attached := e.state.descriptors[index.address].(*relationDescriptor)
	row := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.rowType)}].(*typeDescriptor)
	array := e.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.array)}].(*typeDescriptor)
	if next.owner != actor.Ref || attached.owner != actor.Ref || row.owner != actor.Ref || array.owner != actor.Ref || d.owner == actor.Ref || index.owner == actor.Ref || attached.sqlIndex != index.sqlIndex || e.indexRoots[index.address] != root || e.storage[d.address] != store {
		t.Fatal("owner group not coherent or roots rebuilt")
	}
	expected := []authorization.ACLEntry{
		{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege},
		{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}
	for _, typ := range []*typeDescriptor{row, array} {
		if len(typ.acl.Entries) != len(expected) {
			t.Fatal("dependent type lost or added ACL origins", typ.Name(), typ.acl)
		}
		for _, entry := range expected {
			if !slices.Contains(typ.acl.Entries, entry) {
				t.Fatal("dependent type ACL ownership origin not transferred", typ.Name(), typ.acl)
			}
		}
		if descriptorRoleDependencies(typ, d.owner, nil) {
			t.Fatal("dependent type prevents former owner's retirement", typ.Name())
		}
	}
	if row == oldRow || array == oldArray || oldRow.owner != d.owner || oldArray.owner != d.owner || !reflect.DeepEqual(oldRow.acl, rowACL) || !reflect.DeepEqual(oldArray.acl, arrayACL) {
		t.Fatal("ownership transfer mutated retained dependent type ACL snapshots")
	}
}
func TestMaterializedAlterExtensionAuthority(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	d := refreshUnitRelation(t, e)
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	next := e.state.descriptors[d.address].(*relationDescriptor)
	if len(d.autoExtensions) != 0 || !reflect.DeepEqual(next.autoExtensions, []ids.ObjectAddress{ext.address}) {
		t.Fatal("authority not immutable whole genuine provider")
	}
	if err := materializedAlterUnit(t, e, op, next, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	if e.state.descriptors[d.address] != next {
		t.Fatal("duplicate edge replaced descriptor")
	}
	for _, addresses := range [][]ids.ObjectAddress{{{ClassID: ids.ProcedureClassID, ObjectID: ext.members[0].ObjectID}}, {ext.address, ext.address}, {{ClassID: ids.ExtensionClassID, ObjectID: ext.address.ObjectID, SubID: 1}}, {{ClassID: ids.ExtensionClassID, ObjectID: 999999}}} {
		bad := *next
		bad.autoExtensions = addresses
		if err := (relationProvider{}).Validate(e.CatalogRead(), &bad); err == nil {
			t.Fatal("malformed authority accepted", addresses)
		}
	}
	edges, err := collectPGDepend(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	x := 0
	for _, edge := range edges {
		if edge.dependent == d.address && edge.referenced == ext.address && edge.deptype == 'x' {
			x++
		}
	}
	if x != 1 {
		t.Fatal("wrong auto-extension projection", x)
	}
	extensionUnitError(t, materializedAlterUnit(t, e, op, next, "no_depends", "absent"), "42704")
	if err := materializedAlterUnit(t, e, op, next, "no_depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	detached := e.state.descriptors[d.address].(*relationDescriptor)
	if len(detached.autoExtensions) != 0 || len(next.autoExtensions) != 1 {
		t.Fatal("detach mutated old authority")
	}
	if err := materializedAlterUnit(t, e, op, detached, "no_depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	if e.state.descriptors[d.address] != detached {
		t.Fatal("absent edge replaced descriptor")
	}
}
func TestMaterializedAlterExtensionRetirement(t *testing.T) {
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		t.Run([]string{"restrict", "cascade"}[int(mode)], func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			d := refreshUnitRelation(t, e)
			index := materializedAlterIndex(t, e, op, d)
			old := e.storage[d.address]
			root := e.indexRoots[index.address]
			if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			roots := append([]ids.ObjectAddress{ext.address}, ext.members[:]...)
			for _, a := range roots {
				if err := stage.Remove(e.domain.Ref(stage.base.Epoch(), a)); err != nil {
					t.Fatal(err)
				}
			}
			if err := stageExpressionDependencies(context.Background(), stage, roots, mode, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal("genuine provider retirement", err)
			}
			queryCreatePublish(t, e, stage)
			if e.state.descriptors[d.address] != nil || e.storage[d.address] != nil || e.state.descriptors[index.address] != nil || e.indexRoots[index.address] != nil || len(old.versions) != 3 || root == nil {
				t.Fatal("retirement failed whole group or mutated captured storage")
			}
		})
	}
	t.Run("late_attachment", func(t *testing.T) {
		e, op, ext := extensionUnitInstalled(t)
		d := refreshUnitRelation(t, e)
		store := e.storage[d.address]
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		roots := append([]ids.ObjectAddress{ext.address}, ext.members[:]...)
		for _, a := range roots {
			if err := stage.Remove(e.domain.Ref(stage.base.Epoch(), a)); err != nil {
				t.Fatal(err)
			}
		}
		if err := stageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		changes := newCatalogChanges()
		e.mu.Lock()
		err = e.captureCatalogStageLocked(stage, changes, e.state, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
			t.Fatal(err)
		}
		current := e.state.descriptors[d.address]
		state := e.state
		e.mu.Lock()
		err = e.catalogPreflightLocked(changes)
		e.mu.Unlock()
		if _, conflict := err.(CatalogConflict); !conflict {
			t.Fatal("retained-message retirement missed late x authority", err)
		}
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err == nil {
			t.Fatal("late x edge allowed stale retirement")
		}
		if e.state != state || e.state.descriptors[d.address] != current || e.state.descriptors[ext.address] != ext || e.storage[d.address] != store {
			t.Fatal("late attachment fence leaked retirement")
		}
	})
	t.Run("temporary_provider", func(t *testing.T) {
		e, _, op, ext, closeOwner := extensionTemporaryInstalled(t)
		d := refreshUnitRelation(t, e)
		index := materializedAlterIndex(t, e, op, d)
		store := e.storage[d.address]
		if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
			t.Fatal(err)
		}
		closeOwner()
		if e.state.descriptors[ext.address] != nil || e.state.descriptors[d.address] != nil || e.state.descriptors[index.address] != nil || e.storage[d.address] != nil || e.indexRoots[index.address] != nil || len(store.versions) != 3 {
			t.Fatal("real temporary provider end did not retire auto-extension group")
		}
	})
}
func TestMaterializedAlterDependencyUnion(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	roleExecute(t, e, op, "CREATE MATERIALIZED VIEW union_mv AS SELECT unaccent('Hôtel'::text) AS s")
	d := e.state.descriptors[e.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.targetNamespace, Name: "union_mv"}]].(*relationDescriptor)
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "CREATE VIEW union_child AS SELECT s FROM union_mv")
	child := e.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.namespace, Name: "union_child"}]
	member := e.state.names[ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.targetNamespace, Name: "unaccent", Signature: string(unaccentTextSignature)}]
	survivor := refreshUnitRelation(t, e)
	store := e.storage[survivor.address]
	before := e.state
	providerRoots := append([]ids.ObjectAddress{ext.address}, ext.members[:]...)
	for _, roots := range [][]ids.ObjectAddress{{member, ext.address}, {ext.address, member}} {
		stage := e.BeginCatalogStage(e.CatalogRead())
		for _, a := range providerRoots {
			if err := stage.Remove(e.domain.Ref(stage.base.Epoch(), a)); err != nil {
				t.Fatal(err)
			}
		}
		edges, err := domainDependencyClosure(context.Background(), stage, roots)
		if err != nil {
			t.Fatal(err)
		}
		automatic, ordinary := 0, 0
		for _, edge := range edges {
			if edge.address == d.address {
				if !edge.automatic || edge.target != ext.address || edge.root != ext.address {
					t.Fatal("normal edge won over x", edge)
				}
				automatic++
			}
			if edge.address == child {
				if edge.automatic {
					t.Fatal("automatic flavor leaked into descendant")
				}
				ordinary++
			}
		}
		if automatic != 1 || ordinary != 1 {
			t.Fatal("incomplete merged closure", automatic, ordinary)
		}
		if err := stageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err == nil {
			t.Fatal("ordinary dependent lost RESTRICT authority")
		}
		e.DiscardCatalogStage(stage)
		if e.state != before || e.storage[survivor.address] != store {
			t.Fatal("RESTRICT changed live state")
		}
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	for _, a := range providerRoots {
		if err := stage.Remove(e.domain.Ref(stage.base.Epoch(), a)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageExpressionDependencies(context.Background(), stage, providerRoots, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	if e.state.descriptors[d.address] != nil || e.state.descriptors[child] != nil || e.state.descriptors[ext.address] != nil || e.storage[survivor.address] != store || e.state.descriptors[survivor.address] != survivor {
		t.Fatal("union CASCADE failed or removed unrelated materialization")
	}
}
func TestMaterializedAlterPublication(t *testing.T) {
	for _, reason := range []string{"provider", "cancel", "role", "authorization"} {
		t.Run(reason, func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			d := refreshUnitRelation(t, e)
			store := e.storage[d.address]
			next := *d
			next.revision++
			next.autoExtensions = []ids.ObjectAddress{ext.address}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			stage.querySources = []Descriptor{ext}
			stage.authSession = op.session
			stage.authRevision = op.session.authorization.revision
			stage.roleRevision = e.roles.revision
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "provider":
				copy := *ext
				copy.revision++
				e.state.descriptors[ext.address] = &copy
			case "cancel":
				cancel()
			case "role":
				e.roles.revision++
			case "authorization":
				op.session.authorization.revision++
			}
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if err == nil {
				t.Fatal("unfenced publication")
			}
			if reason == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.state.descriptors[d.address] != d || e.storage[d.address] != store || len(d.autoExtensions) != 0 {
				t.Fatal("failed publication leaked")
			}
		})
	}
}
func TestMaterializedAlterIndexRefreshPublication(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	index := materializedAlterIndex(t, e, op, d)
	d = e.state.descriptors[d.address].(*relationDescriptor)
	for _, count := range []int{4, 0, 2} {
		old := e.storage[d.address]
		root := e.indexRoots[index.address]
		oldEntries := slices.Clone(root.entries)
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := e.stageMaterializedRows(context.Background(), stage, d, old, refreshUnitRows(t, d, count), count != 0, TxRef{}); err != nil {
			t.Fatal(err)
		}
		candidate := stage.materializedReplacement.storage
		queryCreatePublish(t, e, stage)
		d = e.state.descriptors[d.address].(*relationDescriptor)
		next := e.indexRoots[index.address]
		if next == root || e.storage[d.address] != candidate || len(next.entries) != count || !reflect.DeepEqual(oldEntries, root.entries) {
			t.Fatal("roots and storage not atomically rebuilt", count)
		}
	}
	old := e.storage[d.address]
	root := e.indexRoots[index.address]
	state := e.state
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	rows := refreshUnitRows(t, d, 2)
	rows[1] = rows[0]
	if err := e.stageMaterializedRows(context.Background(), stage, d, old, rows, true, TxRef{}); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	extensionUnitError(t, err, "23505")
	if e.state != state || e.storage[d.address] != old || e.indexRoots[index.address] != root {
		t.Fatal("duplicate refresh partially published")
	}
}
func TestMaterializedAlterControlRemap(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	d := refreshUnitRelation(t, e)
	index := materializedAlterIndex(t, e, op, d)
	sourceStore := e.storage[d.address]
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	d = e.state.descriptors[d.address].(*relationDescriptor)
	if remapExtensionDescriptor(d, nil) != d {
		t.Fatal("unnecessary remap allocation")
	}
	target := New()
	targetOp, done := roleOperation(t, target, "postgres")
	defer done()
	roleExecute(t, target, targetOp, "CREATE TEMP TABLE remap_retained(i INT)")
	roleExecute(t, target, targetOp, "INSERT INTO remap_retained VALUES(99)")
	temporary := target.state.descriptors[targetOp.session.temporaryNamespace]
	retained := target.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: temporary.Address(), Name: "remap_retained"}]
	retainedStore := target.storage[retained]
	if temporary.Address().ObjectID != ext.address.ObjectID {
		t.Fatal("fixture did not create a genuine cross-class retained TEMP collision")
	}
	if _, err := target.ReplaceCatalog(context.Background(), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	importedExt := target.state.descriptors[target.state.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}]].(*extensionDescriptor)
	imported := target.state.descriptors[target.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.namespace, Name: d.name}]].(*relationDescriptor)
	importedIndex := target.state.descriptors[target.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.namespace, Name: index.name}]].(*relationDescriptor)
	root := target.indexRoots[importedIndex.address]
	if importedExt.address == ext.address || !reflect.DeepEqual(imported.autoExtensions, []ids.ObjectAddress{importedExt.address}) || importedIndex.sqlIndex.parent != imported.address || root == nil || len(root.entries) != 3 || len(target.storage[imported.address].versions) != 3 {
		t.Fatal("genuine displaced import lost provider/x/index/rows")
	}
	if target.state.descriptors[temporary.Address()] != temporary || target.storage[retained] != retainedStore || e.state.descriptors[d.address] != d || e.storage[d.address] != sourceStore || d.autoExtensions[0] != ext.address {
		t.Fatal("import mutated retained TEMP or source authority")
	}
	remapped := remapExtensionDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{ext.address: importedExt.address}).(*relationDescriptor)
	if remapped == d || remapped.autoExtensions[0] != importedExt.address || d.autoExtensions[0] != ext.address || remapped.view != d.view {
		t.Fatal("remap lost immutable genuine x authority")
	}
	stage := target.BeginCatalogStage(target.CatalogRead())
	defer target.DiscardCatalogStage(stage)
	roots := append([]ids.ObjectAddress{importedExt.address}, importedExt.members[:]...)
	for _, address := range roots {
		if err := stage.Remove(target.domain.Ref(stage.base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, target, stage)
	if target.state.descriptors[imported.address] != nil || target.state.descriptors[importedIndex.address] != nil || target.indexRoots[importedIndex.address] != nil || target.storage[retained] != retainedStore || target.state.descriptors[temporary.Address()] != temporary || e.state.descriptors[d.address] != d {
		t.Fatal("remapped x retirement did not preserve unrelated captured authority")
	}
	predecessor, err := dumpPredecessor(e.CatalogRead(), ext.address, d.address)
	if err != nil || predecessor != ext.address.ObjectID {
		t.Fatal("missing provider dump predecessor", predecessor, err)
	}
}

func TestMaterializedAlterQueuedAuthority(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	admin, adminDone := roleOperation(t, e, "postgres")
	defer adminDone()
	actor := roleAttributeCreate(t, e, admin, "mv_queued_owner", true, parser.RoleOptions{})
	next := *d
	next.owner = actor.Ref
	next.revision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	d = e.state.descriptors[d.address].(*relationDescriptor)
	owner, ownerDone := roleOperation(t, e, "mv_queued_owner")
	defer ownerDone()
	owner.Done()
	withPublicSchemaUnitOwner(t, e, "mv_queued_owner", func() {
		holder := sessionStart(t, e, nil)
		tx := transactionBegin(t, holder)
		ended := false
		defer func() {
			if !ended {
				transactionEnd(t, holder, tx, false)
			}
		}()
		result := sessionSQL(t, holder, "SELECT i FROM unit_mv")
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := lockAsync(e, owner.session, ctx, "ALTER MATERIALIZED VIEW unit_mv RENAME TO saved", false)
		joined := false
		defer func() {
			if !joined {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("queued ALTER did not join")
				}
			}
		}()
		lockPending(t, e, 1)
		changed := *d
		changed.owner = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		changed.revision++
		stage = e.BeginCatalogStage(e.CatalogRead())
		if err := stage.Replace(&changed); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, stage)
		original := e.state.descriptors[d.address]
		store := e.storage[d.address]
		transactionEnd(t, holder, tx, false)
		ended = true
		select {
		case err := <-done:
			joined = true
			extensionUnitError(t, err, "42501")
		case <-time.After(5 * time.Second):
			t.Fatal("queued ALTER did not terminate after grant")
		}
		if e.state.descriptors[d.address] != original || e.storage[d.address] != store {
			t.Fatal("queued actor used stale owner authority")
		}
		lockEmpty(t, e)
	})
}
