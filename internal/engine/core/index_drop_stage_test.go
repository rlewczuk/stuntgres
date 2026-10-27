package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// This fixture admits a real lexical command and retains its authorized inputs;
// no query/inquiry is needed to collect the retiring constraint identities.
func partitionProjectionStage(t *testing.T, interested bool, tag string, definitions ...string) (*core.Engine, *session.Operation, *core.StatementScope, *core.CatalogStage) {
	t.Helper()
	e := engine.New()
	for _, q := range definitions {
		enginetest.Run(t, e, q)
	}
	if interested {
		enginetest.Run(t, e, "CREATE FUNCTION projection_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
		enginetest.Run(t, e, "CREATE EVENT TRIGGER projection_drop ON sql_drop EXECUTE FUNCTION projection_drop()")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "SET search_path = pg_catalog,public")
	enginetest.RoleExecute(t, e, op, "BEGIN")
	t.Cleanup(func() { enginetest.RoleExecute(t, e, op, "ROLLBACK") })
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	// StartStatement owns admission, but Gate attaches the live transaction
	// incarnation and lock ticket before entering the lexical utility command.
	command := "ALTER TABLE projection_root DROP CONSTRAINT projection_pk"
	if tag == "DROP INDEX" {
		command = "DROP INDEX projection_parent"
	}
	if err := scope.Gate(enginetest.Statement(t, command)); err != nil {
		t.Fatal(err)
	}
	previous, err := scope.BeginEventCommand(tag)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.FinishEventCommand(previous, context.Canceled) })
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	path, err := core.ReferencePath(auth)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.DependencyPath = path
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if !reflect.DeepEqual(stage.DropRoles, auth.Roles) || !slices.Equal(stage.DependencyPath, path) {
		t.Fatal("stage did not retain admitted identity inputs")
	}
	return e, op, scope, stage
}

func partitionProjectionRelation(t *testing.T, stage *core.CatalogStage, name string) *catalog.Relation {
	t.Helper()
	a := stage.StageRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	return stage.StageRead().MustRelation(a)
}

func partitionProjectionNoCommandRecords(t *testing.T, scope *core.StatementScope) {
	t.Helper()
	if scope.EventCommand != nil && (scope.EventCommand.Dropped || len(scope.EventCommand.Records) != 0) {
		t.Fatal("private projection admitted command records early", scope.EventCommand)
	}
}

func partitionProjectionConstraint(t *testing.T, got catalog.DroppedObject, oid ids.OID, table, name string) {
	t.Helper()
	if got.Address != (ids.ObjectAddress{ClassID: 2606, ObjectID: oid}) || !got.Found || got.Kind != "table constraint" || got.HasName || !got.HasSchema || got.Schema != "public" || got.Temporary || got.Identity != name+" on public."+table || !slices.Equal(got.Names, []string{"public", table, name}) || got.Args == nil || len(got.Args) != 0 {
		t.Fatal("incorrect retiring constraint identity", got)
	}
}

func TestPartitionDropRelationProjection(t *testing.T) {
	t.Run("authorized_success_sequential_old_immutable", func(t *testing.T) {
		e, _, scope, stage := partitionProjectionStage(t, true, "ALTER TABLE",
			"CREATE TABLE projection_root(k INT,payload INT,CONSTRAINT projection_pk PRIMARY KEY(k),CONSTRAINT projection_other UNIQUE(k,payload)) PARTITION BY RANGE(k)")
		old := partitionProjectionRelation(t, stage, "projection_root")
		oldUnique := slices.Clone(old.Constraints.Unique)
		oldNotNull := slices.Clone(old.Constraints.NotNull)
		oldAttributes := slices.Clone(old.Attributes)
		// An earlier genuine descriptor edit changes the entering identity. Base
		// reconstruction would report projection_pk instead of this new name.
		entering := *old
		entering.Constraints.Unique = slices.Clone(old.Constraints.Unique)
		entering.Constraints.Unique[0].Name = "projection_entering"
		entering.ObjectRevision = old.Revision() + 1
		if err := index.StagePartitionDescriptor(stage, &entering); err != nil {
			t.Fatal(err)
		}
		first := entering.Constraints.Unique[0]
		second := entering.Constraints.Unique[1]
		if err := index.StagePartitionIndexDrop(stage, stage.StageRead().MustRelation(first.Index), index.ConstraintDrivenDrop); err != nil {
			t.Fatal(err)
		}
		if len(stage.DropRecords) != 1 {
			t.Fatal("projection did not capture exactly its retired payload", stage.DropRecords)
		}
		partitionProjectionConstraint(t, stage.DropRecords[0], first.OID, old.Name(), first.Name)
		retained := stage.DropRecords[0]
		retainedNames := slices.Clone(retained.Names)
		middle := stage.StageRead().MustRelation(old.Address())
		if middle.Revision() != entering.Revision()+1 || len(middle.Constraints.Unique) != 1 || middle.Constraints.Unique[0].OID != second.OID || !reflect.DeepEqual(middle.Constraints.NotNull, oldNotNull) || !slices.Equal(middle.Attributes, oldAttributes) {
			t.Fatal("projection changed surviving metadata", middle)
		}
		if err := index.StagePartitionIndexDrop(stage, stage.StageRead().MustRelation(second.Index), index.ConstraintDrivenDrop); err != nil {
			t.Fatal(err)
		}
		if len(stage.DropRecords) != 2 {
			t.Fatal("sequential projection recaptured retired or surviving payloads", stage.DropRecords)
		}
		partitionProjectionConstraint(t, stage.DropRecords[1], second.OID, old.Name(), second.Name)
		last := stage.StageRead().MustRelation(old.Address())
		if last.Revision() != middle.Revision()+1 || len(last.Constraints.Unique) != 0 || len(middle.Constraints.Unique) != 1 || len(entering.Constraints.Unique) != 2 || !reflect.DeepEqual(old.Constraints.Unique, oldUnique) || !reflect.DeepEqual(old.Constraints.NotNull, oldNotNull) || !slices.Equal(old.Attributes, oldAttributes) || !reflect.DeepEqual(stage.DropRecords[0], retained) || !slices.Equal(retained.Names, retainedNames) || e.CatalogRead().MustRelation(old.Address()) != old {
			t.Fatal("projection mutated an immutable predecessor or record")
		}
		for _, r := range stage.DropRecords {
			if r.Original || r.Normal {
				t.Fatal("constraint-owned projection invented provenance", r)
			}
		}
		partitionProjectionNoCommandRecords(t, scope)
	})

	t.Run("constraint_owned_success_only", func(t *testing.T) {
		e, op, scope, stage := partitionProjectionStage(t, true, "ALTER TABLE",
			"CREATE TABLE projection_root(k INT,payload INT,CONSTRAINT projection_pk PRIMARY KEY(k)) PARTITION BY RANGE(k)",
			"CREATE TABLE projection_child PARTITION OF projection_root(CONSTRAINT projection_local UNIQUE(payload)) DEFAULT")
		root := partitionProjectionRelation(t, stage, "projection_root")
		child := partitionProjectionRelation(t, stage, "projection_child")
		selected := root.Constraints.Unique[0]
		var inherited, local catalog.UniqueConstraint
		for _, c := range child.Constraints.Unique {
			if c.Parent == selected.OID {
				inherited = c
			} else {
				local = c
			}
		}
		if inherited.OID == 0 || local.OID == 0 {
			t.Fatal("missing real inherited/local owners")
		}
		action := enginetest.Statement(t, "ALTER TABLE projection_root DROP CONSTRAINT projection_pk").(*parser.AlterTable).Actions[0]
		if _, err := partition.StageConstraintDrop(op, stage, &stage.Candidate, root, action); err != nil {
			t.Fatal(err)
		}
		if len(stage.DropRecords) != 2 || stage.RemovalCount() != 2 {
			t.Fatal("constraint hierarchy lost projection or index effects", stage.DropRecords, stage.RemovalCount())
		}
		for _, r := range stage.DropRecords {
			c, owner := selected, root.Name()
			if r.Address.ObjectID == inherited.OID {
				c, owner = inherited, child.Name()
			}
			partitionProjectionConstraint(t, r, c.OID, owner, c.Name)
		}
		if got := stage.StageRead().MustRelation(child.Address()); len(got.Constraints.Unique) != 1 || got.Constraints.Unique[0].OID != local.OID || !reflect.DeepEqual(got.Constraints.NotNull, child.Constraints.NotNull) {
			t.Fatal("constraint closure removed unrelated local or NOT NULL metadata", got)
		}
		partitionProjectionNoCommandRecords(t, scope)
		if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
			t.Fatal(err)
		}
		if err := e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone()); err != nil {
			t.Fatal(err)
		}
		if !scope.EventCommand.Dropped || len(scope.EventCommand.Records) != 4 {
			t.Fatal("successful transfer did not admit complete hierarchy", scope.EventCommand)
		}
		for _, r := range scope.EventCommand.Records {
			wantOriginal := r.Address == (ids.ObjectAddress{ClassID: 2606, ObjectID: selected.OID})
			if r.Original != wantOriginal || r.Normal {
				t.Fatal("constraint roots spread to recursive owners/indexes", r)
			}
		}
		if e.CatalogRead().MustRelation(root.Address()) != root || e.CatalogRead().MustRelation(child.Address()) != child {
			t.Fatal("private overlay published committed descriptors")
		}
	})

	t.Run("index_reverse_owner", func(t *testing.T) {
		e, op, scope, stage := partitionProjectionStage(t, true, "DROP INDEX",
			"CREATE TABLE projection_root(k INT,payload INT) PARTITION BY RANGE(k)",
			"CREATE TABLE projection_child PARTITION OF projection_root(CONSTRAINT projection_local UNIQUE(k,payload)) DEFAULT",
			"CREATE UNIQUE INDEX projection_parent ON ONLY projection_root(k,payload)",
			"ALTER INDEX projection_parent ATTACH PARTITION projection_local")
		parent := partitionProjectionRelation(t, stage, "projection_parent")
		child := partitionProjectionRelation(t, stage, "projection_child")
		local := child.Constraints.Unique[0]
		for _, idx := range []*catalog.Relation{parent, stage.StageRead().MustRelation(local.Index)} {
			if err := index.StagePartitionIndexDrop(stage, idx, index.IndexDrivenDrop); err != nil {
				t.Fatal(err)
			}
		}
		stage.MarkDrop(parent.Address(), catalog.DropProvenance{Original: true})
		if len(stage.DropRecords) != 1 || stage.RemovalCount() != 2 {
			t.Fatal("reverse retirement lost actual effects", stage.DropRecords)
		}
		partitionProjectionConstraint(t, stage.DropRecords[0], local.OID, child.Name(), local.Name)
		partitionProjectionNoCommandRecords(t, scope)
		if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
			t.Fatal(err)
		}
		if err := e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone()); err != nil {
			t.Fatal(err)
		}
		if len(scope.EventCommand.Records) != 3 {
			t.Fatal("reverse transfer lost a record", scope.EventCommand)
		}
		for _, r := range scope.EventCommand.Records {
			wantOriginal := r.Address == parent.Address()
			wantNormal := r.Address == (ids.ObjectAddress{ClassID: 2606, ObjectID: local.OID})
			if r.Original != wantOriginal || r.Normal != wantNormal {
				t.Fatal("incoming ownership direction did not control provenance", r)
			}
		}
	})

	t.Run("no_interest", func(t *testing.T) {
		_, _, scope, stage := partitionProjectionStage(t, false, "ALTER TABLE", "CREATE TABLE projection_root(k INT PRIMARY KEY) PARTITION BY RANGE(k)")
		root := partitionProjectionRelation(t, stage, "projection_root")
		if err := index.StagePartitionIndexDrop(stage, stage.StageRead().MustRelation(root.Constraints.Unique[0].Index), index.IndexDrivenDrop); err != nil {
			t.Fatal(err)
		}
		if len(stage.StageRead().MustRelation(root.Address()).Constraints.Unique) != 0 || len(stage.DropRecords) != 0 || len(stage.DropFlags) != 0 {
			t.Fatal("no-interest retirement materialized identities or lost effects")
		}
		partitionProjectionNoCommandRecords(t, scope)
	})

	t.Run("checked_replace_failure", func(t *testing.T) {
		e, _, scope, stage := partitionProjectionStage(t, true, "ALTER TABLE", "CREATE TABLE projection_root(k INT PRIMARY KEY) PARTITION BY RANGE(k)")
		root := partitionProjectionRelation(t, stage, "projection_root")
		next := *root
		next.ObjectRevision = root.Revision() + 1
		next.Constraints.Unique = nil
		// Renderable authentic inputs, but a genuinely discarded stage cannot
		// accept the checked replacement. Captures must not be appended first.
		e.DiscardCatalogStage(stage)
		if err := stage.ReplaceRelationProjection(root, &next); err == nil || err.Error() != "stuntgres: invalid catalog stage" {
			t.Fatal("discarded stage accepted replacement", err)
		}
		if len(stage.DropRecords) != 0 || stage.Replacement(root.Address()) != nil || stage.RemovalCount() != 0 || e.CatalogRead().MustRelation(root.Address()) != root {
			t.Fatal("failed replacement retained a projection or record")
		}
		partitionProjectionNoCommandRecords(t, scope)
	})

	t.Run("internal_provider_boundary", func(t *testing.T) {
		_, _, scope, stage := partitionProjectionStage(t, true, "ALTER TABLE", "CREATE TABLE projection_root(k INT PRIMARY KEY) PARTITION BY RANGE(k)")
		root := partitionProjectionRelation(t, stage, "projection_root")
		next := *root
		next.Constraints.Unique = nil
		// This is the incumbent provider's operation-internal boundary, not
		// a fictitious internal mode on the partition adapter.
		p := core.StageDropProvider(nil, stage, scope, nil, nil)
		p.InternalRemoval = true
		records, err := p.CaptureRelationProjection(root, &next)
		if err != nil || len(records) != 0 {
			t.Fatal("internal provider projection escaped", records, err)
		}
		if err := p.RemoveInternal(stage.Owner.Catalog.Domain.Ref(stage.Base.Epoch(), root.Constraints.Unique[0].Index)); err != nil {
			t.Fatal(err)
		}
		for _, removal := range stage.Removals() {
			if !removal.Internal || len(removal.Dropped) != 0 {
				t.Fatal("internal removal admitted command payloads", removal)
			}
		}
		partitionProjectionNoCommandRecords(t, scope)
	})

	t.Run("rejected_selection", func(t *testing.T) {
		_, op, scope, stage := partitionProjectionStage(t, true, "ALTER TABLE",
			"CREATE TABLE projection_root(k INT PRIMARY KEY) PARTITION BY RANGE(k)",
			"CREATE TABLE projection_child PARTITION OF projection_root DEFAULT")
		child := partitionProjectionRelation(t, stage, "projection_child")
		c := child.Constraints.Unique[0]
		action := parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: c.Name}}
		_, err := partition.StageConstraintDrop(op, stage, &stage.Candidate, child, action)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "42P16" || len(stage.DropFlags) != 0 || len(stage.DropRecords) != 0 || stage.RemovalCount() != 0 {
			t.Fatal("rejected inherited selection retained roots/effects", err)
		}
		partitionProjectionNoCommandRecords(t, scope)
	})
}
