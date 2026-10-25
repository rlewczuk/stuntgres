package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/control"
	"testing"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func materializedAlterUnit(t *testing.T, e *Engine, op *session.Operation, d *catalog.Relation, action, target string) error {
	t.Helper()
	return extensionUnitExecute(t, e, op, &parser.AlterView{Materialized: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Action: action, Target: parser.Identifier{Text: target}})
}
func materializedAlterIndex(t *testing.T, e *Engine, op *session.Operation, d *catalog.Relation) *catalog.Relation {
	t.Helper()
	// Setup uses the real index binder and publisher; assertions below inspect roots.
	roleExecute(t, e, op, "CREATE UNIQUE INDEX unit_mv_i ON "+d.Name()+"(i)")
	return e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: "unit_mv_i"}))
}
func TestMaterializedAlterIdentity(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	store := e.Storage.Relations[d.Address()]
	rule, rowType := stored.ViewOf(d).RuleOID(), d.RowType
	if err := materializedAlterUnit(t, e, op, d, "rename", "saved"); err != nil {
		t.Fatal(err)
	}
	next := e.Catalog.State.MustRelation(d.Address())
	if next == d || next.Name() != "saved" || d.Name() != "unit_mv" || next.RowType != rowType || stored.ViewOf(next).RuleOID() != rule || next.View != d.View || e.Storage.Relations[d.Address()] != store || !reflect.DeepEqual(next.Attributes, d.Attributes) {
		t.Fatal("metadata publication reminted or mutated captured identity")
	}
	row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rowType)})
	if row.Name() != "saved" || row.OwningRelation != d.Address() {
		t.Fatal("row type rename not synchronized")
	}
}
func TestMaterializedAlterOwnerAttachments(t *testing.T) {
	e := New()
	d := refreshUnitRelation(t, e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	index := materializedAlterIndex(t, e, op, d)
	root := e.Storage.IndexRoots[index.Address()]
	store := e.Storage.Relations[d.Address()]
	actor := roleAttributeCreate(t, e, op, "mv_next", true, parser.RoleOptions{})
	oldRow := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	oldArray := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oldRow.Array)})
	rowACL, arrayACL := oldRow.ACL, oldArray.ACL
	rowACL.Entries = slices.Clone(rowACL.Entries)
	arrayACL.Entries = slices.Clone(arrayACL.Entries)
	stmt := &parser.AlterView{Materialized: true, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}, Action: "owner", Target: parser.Identifier{Text: "mv_next"}}
	if err := extensionUnitExecute(t, e, op, stmt); err != nil {
		t.Fatal(err)
	}
	next := e.Catalog.State.MustRelation(d.Address())
	attached := e.Catalog.State.MustRelation(index.Address())
	row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})
	array := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if next.Owner != actor.Ref || attached.Owner != actor.Ref || row.Owner != actor.Ref || array.Owner != actor.Ref || d.Owner == actor.Ref || index.Owner == actor.Ref || attached.SQLIndex != index.SQLIndex || e.Storage.IndexRoots[index.Address()] != root || e.Storage.Relations[d.Address()] != store {
		t.Fatal("owner group not coherent or roots rebuilt")
	}
	expected := []authorization.ACLEntry{
		{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege},
		{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}
	for _, typ := range []*catalog.Type{row, array} {
		if len(typ.ACL.Entries) != len(expected) {
			t.Fatal("dependent type lost or added ACL origins", typ.Name(), typ.ACL)
		}
		for _, entry := range expected {
			if !slices.Contains(typ.ACL.Entries, entry) {
				t.Fatal("dependent type ACL ownership origin not transferred", typ.Name(), typ.ACL)
			}
		}
		if access.DescriptorRoleDependencies(typ, d.Owner, nil) {
			t.Fatal("dependent type prevents former owner's retirement", typ.Name())
		}
	}
	if row == oldRow || array == oldArray || oldRow.Owner != d.Owner || oldArray.Owner != d.Owner || !reflect.DeepEqual(oldRow.ACL, rowACL) || !reflect.DeepEqual(oldArray.ACL, arrayACL) {
		t.Fatal("ownership transfer mutated retained dependent type ACL snapshots")
	}
}
func TestMaterializedAlterExtensionAuthority(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	d := refreshUnitRelation(t, e)
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	next := e.Catalog.State.MustRelation(d.Address())
	if len(d.AutoExtensions) != 0 || !reflect.DeepEqual(next.AutoExtensions, []ids.ObjectAddress{ext.Address()}) {
		t.Fatal("authority not immutable whole genuine provider")
	}
	if err := materializedAlterUnit(t, e, op, next, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.State.Get(d.Address()) != next {
		t.Fatal("duplicate edge replaced descriptor")
	}
	for _, addresses := range [][]ids.ObjectAddress{{{ClassID: ids.ProcedureClassID, ObjectID: ext.Members[0].ObjectID}}, {ext.Address(), ext.Address()}, {{ClassID: ids.ExtensionClassID, ObjectID: ext.Address().ObjectID, SubID: 1}}, {{ClassID: ids.ExtensionClassID, ObjectID: 999999}}} {
		bad := *next
		bad.AutoExtensions = addresses
		if err := (RelationProvider()).Validate(e.CatalogRead(), &bad); err == nil {
			t.Fatal("malformed authority accepted", addresses)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), e.CatalogRead(), EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	x := 0
	for _, edge := range edges {
		if edge.Dependent == d.Address() && edge.Referenced == ext.Address() && edge.DepType == 'x' {
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
	detached := e.Catalog.State.MustRelation(d.Address())
	if len(detached.AutoExtensions) != 0 || len(next.AutoExtensions) != 1 {
		t.Fatal("detach mutated old authority")
	}
	if err := materializedAlterUnit(t, e, op, detached, "no_depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.State.Get(d.Address()) != detached {
		t.Fatal("absent edge replaced descriptor")
	}
}
func TestMaterializedAlterExtensionRetirement(t *testing.T) {
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		t.Run([]string{"restrict", "cascade"}[int(mode)], func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			d := refreshUnitRelation(t, e)
			index := materializedAlterIndex(t, e, op, d)
			old := e.Storage.Relations[d.Address()]
			root := e.Storage.IndexRoots[index.Address()]
			if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			roots := append([]ids.ObjectAddress{ext.Address()}, ext.Members[:]...)
			for _, a := range roots {
				if err := stage.Remove(e.Catalog.Domain.Ref(stage.Base.Epoch(), a)); err != nil {
					t.Fatal(err)
				}
			}
			if err := StageExpressionDependencies(context.Background(), stage, roots, mode, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal("genuine provider retirement", err)
			}
			queryCreatePublish(t, e, stage)
			if e.Catalog.State.Get(d.Address()) != nil || e.Storage.Relations[d.Address()] != nil || e.Catalog.State.Get(index.Address()) != nil || e.Storage.IndexRoots[index.Address()] != nil || len(old.Versions) != 3 || root == nil {
				t.Fatal("retirement failed whole group or mutated captured storage")
			}
		})
	}
	t.Run("late_attachment", func(t *testing.T) {
		e, op, ext := extensionUnitInstalled(t)
		d := refreshUnitRelation(t, e)
		store := e.Storage.Relations[d.Address()]
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		roots := append([]ids.ObjectAddress{ext.Address()}, ext.Members[:]...)
		for _, a := range roots {
			if err := stage.Remove(e.Catalog.Domain.Ref(stage.Base.Epoch(), a)); err != nil {
				t.Fatal(err)
			}
		}
		if err := StageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		changes := session.NewJournal()
		e.Mu.Lock()
		err = EngineCaptureCatalogStageLocked(e, stage, changes, e.Catalog.State, nil)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
			t.Fatal(err)
		}
		current := e.Catalog.State.Get(d.Address())
		state := e.Catalog.State
		e.Mu.Lock()
		err = e.CatalogPreflightLocked(changes)
		e.Mu.Unlock()
		if _, conflict := err.(catalog.Conflict); !conflict {
			t.Fatal("retained-message retirement missed late x authority", err)
		}
		if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
			t.Fatal("late x edge allowed stale retirement")
		}
		if e.Catalog.State != state || e.Catalog.State.Get(d.Address()) != current || e.Catalog.State.Get(ext.Address()) != ext || e.Storage.Relations[d.Address()] != store {
			t.Fatal("late attachment fence leaked retirement")
		}
	})
	t.Run("temporary_provider", func(t *testing.T) {
		e, _, op, ext, closeOwner := extensionTemporaryInstalled(t)
		d := refreshUnitRelation(t, e)
		index := materializedAlterIndex(t, e, op, d)
		store := e.Storage.Relations[d.Address()]
		if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
			t.Fatal(err)
		}
		closeOwner()
		if e.Catalog.State.Get(ext.Address()) != nil || e.Catalog.State.Get(d.Address()) != nil || e.Catalog.State.Get(index.Address()) != nil || e.Storage.Relations[d.Address()] != nil || e.Storage.IndexRoots[index.Address()] != nil || len(store.Versions) != 3 {
			t.Fatal("real temporary provider end did not retire auto-extension group")
		}
	})
}
func TestMaterializedAlterDependencyUnion(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	roleExecute(t, e, op, "CREATE MATERIALIZED VIEW union_mv AS SELECT unaccent('Hôtel'::text) AS s")
	d := e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "union_mv"}))
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "CREATE VIEW union_child AS SELECT s FROM union_mv")
	child := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: "union_child"})
	member := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "unaccent", Signature: string(catalog.UnaccentTextSignature)})
	survivor := refreshUnitRelation(t, e)
	store := e.Storage.Relations[survivor.Address()]
	before := e.Catalog.State
	providerRoots := append([]ids.ObjectAddress{ext.Address()}, ext.Members[:]...)
	for _, roots := range [][]ids.ObjectAddress{{member, ext.Address()}, {ext.Address(), member}} {
		stage := e.BeginCatalogStage(e.CatalogRead())
		for _, a := range providerRoots {
			if err := stage.Remove(e.Catalog.Domain.Ref(stage.Base.Epoch(), a)); err != nil {
				t.Fatal(err)
			}
		}
		edges, err := DomainDependencyClosure(context.Background(), stage, roots)
		if err != nil {
			t.Fatal(err)
		}
		automatic, ordinary := 0, 0
		for _, edge := range edges {
			if edge.Address == d.Address() {
				if !edge.Automatic || edge.Target != ext.Address() || edge.Root != ext.Address() {
					t.Fatal("normal edge won over x", edge)
				}
				automatic++
			}
			if edge.Address == child {
				if edge.Automatic {
					t.Fatal("automatic flavor leaked into descendant")
				}
				ordinary++
			}
		}
		if automatic != 1 || ordinary != 1 {
			t.Fatal("incomplete merged closure", automatic, ordinary)
		}
		if err := StageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err == nil {
			t.Fatal("ordinary dependent lost RESTRICT authority")
		}
		e.DiscardCatalogStage(stage)
		if e.Catalog.State != before || e.Storage.Relations[survivor.Address()] != store {
			t.Fatal("RESTRICT changed live state")
		}
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	for _, a := range providerRoots {
		if err := stage.Remove(e.Catalog.Domain.Ref(stage.Base.Epoch(), a)); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageExpressionDependencies(context.Background(), stage, providerRoots, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	if e.Catalog.State.Get(d.Address()) != nil || e.Catalog.State.Get(child) != nil || e.Catalog.State.Get(ext.Address()) != nil || e.Storage.Relations[survivor.Address()] != store || e.Catalog.State.Get(survivor.Address()) != survivor {
		t.Fatal("union CASCADE failed or removed unrelated materialization")
	}
}
func TestMaterializedAlterPublication(t *testing.T) {
	for _, reason := range []string{"provider", "cancel", "role", "authorization"} {
		t.Run(reason, func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			d := refreshUnitRelation(t, e)
			store := e.Storage.Relations[d.Address()]
			next := *d
			next.ObjectRevision++
			next.AutoExtensions = []ids.ObjectAddress{ext.Address()}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			stage.QuerySources = []catalog.Descriptor{ext}
			stage.AuthSession = op.Session
			stage.AuthRevision = op.Session.Authorization.Revision
			stage.RoleRevision = e.RoleStore.Published.Revision
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
				copy.ObjectRevision++
				e.Catalog.State.SetDescriptor(ext.Address(), &copy)
			case "cancel":
				cancel()
			case "role":
				e.RoleStore.Published.Revision++
			case "authorization":
				op.Session.Authorization.Revision++
			}
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil {
				t.Fatal("unfenced publication")
			}
			if reason == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Catalog.State.Get(d.Address()) != d || e.Storage.Relations[d.Address()] != store || len(d.AutoExtensions) != 0 {
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
	d = e.Catalog.State.MustRelation(d.Address())
	for _, count := range []int{4, 0, 2} {
		old := e.Storage.Relations[d.Address()]
		root := e.Storage.IndexRoots[index.Address()]
		oldEntries := slices.Clone(root.Entries)
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := stageMaterializedRows(e, context.Background(), stage, d, old, refreshUnitRows(t, d, count), count != 0, ids.TxRef{}); err != nil {
			t.Fatal(err)
		}
		candidate := stage.MaterializedOrZero().Replacement.Storage
		queryCreatePublish(t, e, stage)
		d = e.Catalog.State.MustRelation(d.Address())
		next := e.Storage.IndexRoots[index.Address()]
		if next == root || e.Storage.Relations[d.Address()] != candidate || len(next.Entries) != count || !reflect.DeepEqual(oldEntries, root.Entries) {
			t.Fatal("roots and storage not atomically rebuilt", count)
		}
	}
	old := e.Storage.Relations[d.Address()]
	root := e.Storage.IndexRoots[index.Address()]
	state := e.Catalog.State
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	rows := refreshUnitRows(t, d, 2)
	rows[1] = rows[0]
	if err := stageMaterializedRows(e, context.Background(), stage, d, old, rows, true, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	extensionUnitError(t, err, "23505")
	if e.Catalog.State != state || e.Storage.Relations[d.Address()] != old || e.Storage.IndexRoots[index.Address()] != root {
		t.Fatal("duplicate refresh partially published")
	}
}
func TestMaterializedAlterControlRemap(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	d := refreshUnitRelation(t, e)
	index := materializedAlterIndex(t, e, op, d)
	sourceStore := e.Storage.Relations[d.Address()]
	if err := materializedAlterUnit(t, e, op, d, "depends", "unaccent"); err != nil {
		t.Fatal(err)
	}
	d = e.Catalog.State.MustRelation(d.Address())
	if extension.RemapDescriptor(d, nil) != d {
		t.Fatal("unnecessary remap allocation")
	}
	target := New()
	targetOp, done := roleOperation(t, target, "postgres")
	defer done()
	roleExecute(t, target, targetOp, "CREATE TEMP TABLE remap_retained(i INT)")
	roleExecute(t, target, targetOp, "INSERT INTO remap_retained VALUES(99)")
	temporary := target.Catalog.State.Get(targetOp.Session.TemporaryNamespace)
	retained := target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: temporary.Address(), Name: "remap_retained"})
	retainedStore := target.Storage.Relations[retained]
	if temporary.Address().ObjectID != ext.Address().ObjectID {
		t.Fatal("fixture did not create a genuine cross-class retained TEMP collision")
	}
	if _, err := target.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	importedExt := target.Catalog.State.MustExtension(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	imported := target.Catalog.State.MustRelation(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}))
	importedIndex := target.Catalog.State.MustRelation(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: index.Name()}))
	root := target.Storage.IndexRoots[importedIndex.Address()]
	if importedExt.Address() == ext.Address() || !reflect.DeepEqual(imported.AutoExtensions, []ids.ObjectAddress{importedExt.Address()}) || importedIndex.SQLIndex.Parent != imported.Address() || root == nil || len(root.Entries) != 3 || len(target.Storage.Relations[imported.Address()].Versions) != 3 {
		t.Fatal("genuine displaced import lost provider/x/index/rows")
	}
	if target.Catalog.State.Get(temporary.Address()) != temporary || target.Storage.Relations[retained] != retainedStore || e.Catalog.State.Get(d.Address()) != d || e.Storage.Relations[d.Address()] != sourceStore || d.AutoExtensions[0] != ext.Address() {
		t.Fatal("import mutated retained TEMP or source authority")
	}
	remapped := extension.RemapDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): importedExt.Address()}).(*catalog.Relation)
	if remapped == d || remapped.AutoExtensions[0] != importedExt.Address() || d.AutoExtensions[0] != ext.Address() || remapped.View != d.View {
		t.Fatal("remap lost immutable genuine x authority")
	}
	stage := target.BeginCatalogStage(target.CatalogRead())
	defer target.DiscardCatalogStage(stage)
	roots := append([]ids.ObjectAddress{importedExt.Address()}, importedExt.Members[:]...)
	for _, address := range roots {
		if err := stage.Remove(target.Catalog.Domain.Ref(stage.Base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageExpressionDependencies(context.Background(), stage, roots, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, target, stage)
	if target.Catalog.State.Get(imported.Address()) != nil || target.Catalog.State.Get(importedIndex.Address()) != nil || target.Storage.IndexRoots[importedIndex.Address()] != nil || target.Storage.Relations[retained] != retainedStore || target.Catalog.State.Get(temporary.Address()) != temporary || e.Catalog.State.Get(d.Address()) != d {
		t.Fatal("remapped x retirement did not preserve unrelated captured authority")
	}
	predecessor, err := control.Resolver{}.Predecessor(e.CatalogRead(), ext.Address(), d.Address())
	if err != nil || predecessor != ext.Address().ObjectID {
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
	next.Owner = actor.Ref
	next.ObjectRevision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	d = e.Catalog.State.MustRelation(d.Address())
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
		done := lockAsync(e, owner.Session, ctx, "ALTER MATERIALIZED VIEW unit_mv RENAME TO saved", false)
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
		changed.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		changed.ObjectRevision++
		stage = e.BeginCatalogStage(e.CatalogRead())
		if err := stage.Replace(&changed); err != nil {
			t.Fatal(err)
		}
		queryCreatePublish(t, e, stage)
		original := e.Catalog.State.Get(d.Address())
		store := e.Storage.Relations[d.Address()]
		transactionEnd(t, holder, tx, false)
		ended = true
		select {
		case err := <-done:
			joined = true
			extensionUnitError(t, err, "42501")
		case <-time.After(5 * time.Second):
			t.Fatal("queued ALTER did not terminate after grant")
		}
		if e.Catalog.State.Get(d.Address()) != original || e.Storage.Relations[d.Address()] != store {
			t.Fatal("queued actor used stale owner authority")
		}
		lockEmpty(t, e)
	})
}
