package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// These are package admission/publication witnesses, not PostgreSQL concurrency
// claims. As in TestInheritanceAlterAdmissionGrowth, a genuine native CREATE
// stage publishes a new descendant while a real descendant AX request is queued.
// No SQL executor, fake catalog pointer, observer hook or sleep drives the race.
type inheritanceMetadataAdmissionCase struct {
	e      *Engine
	op     *Operation
	scope  *StatementScope
	raw    parser.Statement
	auth   AuthorizationView
	cancel context.CancelFunc
}

func inheritanceMetadataScope(t *testing.T, e *Engine, role, sql string) *inheritanceMetadataAdmissionCase {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	principal, err := e.ResolveLogin(role)
	if err != nil {
		t.Fatal(err)
	}
	session, err := e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: principal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.RequestEnd(CleanClose)
		if err := session.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	raw := statement(t, sql)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	if err := scope.gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.acquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return &inheritanceMetadataAdmissionCase{e, op, scope, raw, auth, cancel}
}

func (command *inheritanceMetadataAdmissionCase) run() error {
	command.e.mu.Lock()
	root := command.scope.lockedDescriptor.(*catalog.Relation)
	var err error
	switch raw := command.raw.(type) {
	case *parser.AlterTableDefault:
		_, err = command.e.alterInheritanceDefaultLocked(command.scope, command.op, command.auth, raw, root)
	case *parser.AlterTableRenameColumn:
		_, err = command.e.renameInheritanceColumnLocked(command.scope, command.op, command.auth, root, raw.Column.Text, raw.NewName.Text, raw.Only)
	case *parser.AlterTableRenameConstraint:
		_, err = command.e.renameInheritanceConstraintLocked(command.scope, command.op, command.auth, raw, root, 'n')
	default:
		command.e.mu.Unlock()
		return errors.New("metadata admission fixture has no matching private provider")
	}
	return err
}

func inheritanceMetadataFixture(t *testing.T, actor string) (*Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e, p, c := inheritanceDMLPublishedGraph(t)
	// Use the actual shape provider to install allocated, validated NN facts and
	// live parent/child origins. Parsing supplies input; no public SQL is run.
	nn := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ADD CONSTRAINT metadata_nn NOT NULL i")
	if err := nn.run(); err != nil {
		t.Fatal(err)
	}
	if err := nn.scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	seed := inheritanceMetadataScope(t, e, "postgres", "ALTER TABLE p ALTER i SET DEFAULT 9")
	if err := seed.run(); err != nil {
		t.Fatal(err)
	}
	if err := seed.scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if actor != "postgres" {
		// Role/owner setup is incidental to the package owner-recheck witness.
		for _, sql := range []string{"CREATE ROLE metadata_owner LOGIN", "CREATE ROLE metadata_other LOGIN", "ALTER TABLE p OWNER TO metadata_owner", "ALTER TABLE c OWNER TO metadata_owner"} {
			ownershipUnitExec(t, e, sql)
		}
	}
	view := e.CatalogRead()
	return e, view.MustRelation(p.Address()), view.MustRelation(c.Address())
}

func inheritanceMetadataChild(t *testing.T, e *Engine, parent *catalog.Relation, name string, value int64, owner authorization.PrincipalRef) *catalog.Relation {
	t.Helper()
	// The genuine CREATE provider remaps inherited recipes, allocates fresh
	// stored-definition identities/fact generations and records dependencies.
	// Copying the parent's bound default or NN fact is not a CREATE operation.
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := statement(t, "CREATE TABLE public."+quoteIdentifier(name)+" () INHERITS (public."+quoteIdentifier(parent.Name())+")").(*parser.CreateTable)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var stage *CatalogStage
	var child *catalog.Relation
	if err == nil {
		stage = e.BeginCatalogStage(auth.catalog)
		err = e.schemaTableLocked(op, auth, stage, raw, parent.Namespace(), owner, sqltype.InputEnvironment{}, nil)
	}
	if err == nil {
		address := stage.Names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}]
		child, _ = stage.Descriptors[address].(*catalog.Relation)
		if child == nil {
			err = errors.New("authentic CREATE provider did not stage the descendant")
		}
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	if err := e.stageQueryTableRows(context.Background(), stage, child, [][]sqltype.Value{{inheritanceQueryInt(e, value)}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return e.CatalogRead().MustRelation(child.Address())
}

func inheritanceMetadataWorker(t *testing.T, command *inheritanceMetadataAdmissionCase, holder *StatementScope, child *catalog.Relation) (<-chan error, func()) {
	t.Helper()
	result := make(chan error, 1)
	joined := false
	go func() { result <- command.run() }()
	t.Cleanup(func() {
		command.cancel()
		holder.Finish(nil)
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("metadata admission worker did not join")
			}
		}
	})
	lockPending(t, command.e, 1)
	ref := command.e.catalog.Domain.Ref(command.auth.catalog.Epoch(), child.Address())
	lockState := command.e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == command.scope.ticket && entry.Pending[0].Mode == lock.AccessExclusive
	if !queued {
		t.Fatal("metadata provider did not queue its actual descendant AX request")
	}
	return result, func() { joined = true }
}

var inheritanceMetadataActions = []struct{ name, sql string }{
	{"set_default", "ALTER TABLE p ALTER i SET DEFAULT 7"},
	{"drop_default", "ALTER TABLE p ALTER i DROP DEFAULT"},
	{"column_rename", "ALTER TABLE p RENAME COLUMN i TO renamed_i"},
	{"constraint_rename", "ALTER TABLE p RENAME CONSTRAINT metadata_nn TO renamed_nn"},
}

func inheritanceMetadataAssert(t *testing.T, e *Engine, action string, old, current *catalog.Relation) {
	t.Helper()
	if current == nil || current == old || current.Owner != old.Owner || !slices.Equal(current.Attributes, old.Attributes) || current.ColumnMeta.HighWater != old.ColumnMeta.HighWater || current.Columns[0].Usage != old.Columns[0].Usage || len(current.Constraints.NotNull) != 1 || current.Constraints.NotNull[0].OID != old.Constraints.NotNull[0].OID || current.Constraints.NotNull[0].Attribute != old.Constraints.NotNull[0].Attribute || current.Constraints.NotNull[0].NotValid || current.Constraints.NotNull[0].NoInherit {
		t.Fatal("metadata operation lost actual attribute/owner/fact identity", old.Name(), current)
	}
	columnName, constraintName := "i", "metadata_nn"
	if action == "column_rename" {
		columnName = "renamed_i"
	}
	if action == "constraint_rename" {
		constraintName = "renamed_nn"
	}
	if current.Columns[0].Name != columnName || current.Constraints.NotNull[0].Name != constraintName {
		t.Fatal("admitted descendant missed actual metadata operation", current.Name(), current.Columns, current.Constraints.NotNull)
	}
	definition, present := current.ColumnMeta.Defaults[current.Attributes[0]]
	if action == "drop_default" {
		if present {
			t.Fatal("admitted descendant retained dropped default", current.Name())
		}
	} else {
		if !present {
			t.Fatal("metadata operation lost physical default", current.Name())
		}
		want := int64(9)
		if action == "set_default" {
			want = 7
		}
		value, err := definition.Expression.Eval(&EvalContext{Context: context.Background(), catalog: e.CatalogRead()})
		if err != nil || !reflect.DeepEqual(value, inheritanceQueryInt(e, want)) {
			t.Fatal("wrong independent physical default value", current.Name(), value, err)
		}
	}
	if old.Inheritance != nil {
		before, after := old.Inheritance.Columns[old.Attributes[0]], current.Inheritance.Columns[current.Attributes[0]]
		if before.Local != after.Local || !reflect.DeepEqual(before.Origins, after.Origins) || !reflect.DeepEqual(old.Inheritance.NotNull, current.Inheritance.NotNull) {
			t.Fatal("metadata operation localized or changed live inherited origins", current.Name())
		}
		if (action == "set_default" || action == "drop_default") && !after.ExplicitDefault {
			t.Fatal("physical recipe authority was not recorded independently of locality", current.Name())
		}
	}
}

func TestInheritanceMetadataAdmissionGrowth(t *testing.T) {
	for _, action := range inheritanceMetadataActions {
		for _, sticky := range []bool{false, true} {
			growth := map[bool]string{false: "first_grandchild", true: "further_sticky_growth"}[sticky]
			t.Run(action.name+"/"+growth, func(t *testing.T) {
				e, _, child := inheritanceMetadataFixture(t, "postgres")
				if sticky {
					inheritanceMetadataChild(t, e, child, "initial_grandchild", 30, child.Owner)
					child = e.CatalogRead().MustRelation(child.Address())
				}
				if child.History.HasSubclasses != sticky {
					t.Fatal("fixture has wrong authentic sticky subclass state")
				}
				holder := inheritanceAlterAXHolder(t, e, child)
				command := inheritanceMetadataScope(t, e, "postgres", action.sql)
				result, joined := inheritanceMetadataWorker(t, command, holder, child)
				before := e.CatalogRead()
				late := inheritanceMetadataChild(t, e, child, "late_grandchild", 40, child.Owner)
				grown := e.CatalogRead()
				currentChild := grown.MustRelation(child.Address())
				if grown.Epoch() != before.Epoch() || (!sticky && (currentChild == child || !inheritanceMarkerPublication(child, currentChild) || currentChild.History.SubclassPredecessor != child || currentChild.Revision() != currentChild.History.SubclassPublication)) || sticky && currentChild != child {
					t.Fatal("fixture did not publish genuine additive growth/marker identity")
				}
				originals := inheritanceAlterOrder(grown, grown.MustRelation(command.scope.lockedRef.Address))
				stores := maps.Clone(e.storage.Relations)
				rows := inheritanceAlterRowRoots(e)
				for _, old := range originals {
					if old.Columns[0].Name != "i" || len(old.Constraints.NotNull) != 1 || old.Constraints.NotNull[0].Name != "metadata_nn" {
						t.Fatal("queued metadata provider exposed a partial rename", old.Name())
					}
					definition, present := old.ColumnMeta.Defaults[old.Attributes[0]]
					if !present {
						t.Fatal("queued metadata provider exposed a partial default removal", old.Name())
					}
					value, err := definition.Expression.Eval(&EvalContext{Context: context.Background(), catalog: grown})
					if err != nil || !reflect.DeepEqual(value, inheritanceQueryInt(e, 9)) {
						t.Fatal("queued metadata provider exposed a partial default change", old.Name(), value, err)
					}
				}
				holder.Finish(nil)
				if err := inheritanceAlterJoin(t, result, joined); err != nil {
					t.Fatal("metadata-only admission rejected genuine additive descendant growth", err)
				}
				// Ordinary metadata utilities publish their complete catalog inside
				// the provider; Finish releases statement resources. Observe that
				// incumbent consumer boundary, not the shape-controller's staging.
				completed := e.CatalogRead()
				if completed.Epoch() != grown.Epoch() || completed.Get(late.Address()) == nil {
					t.Fatal("completed utility lost the actual reconciled descendant/lifetime")
				}
				if !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) {
					t.Fatal("completed metadata utility changed physical row values")
				}
				for _, old := range originals {
					ref := e.catalog.Domain.Ref(grown.Epoch(), old.Address())
					if !command.scope.holdsColumnDropGrant(ref) {
						t.Fatal("actual reconciled target lacks AX admission", old.Name())
					}
					current, _ := completed.Relation(old.Address())
					inheritanceMetadataAssert(t, e, action.name, old, current)
					if e.storage.Relations[old.Address()] != stores[old.Address()] {
						t.Fatal("completed metadata utility replaced physical storage", old.Name())
					}
				}
				if err := command.scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				published := e.CatalogRead()
				for _, old := range originals {
					inheritanceMetadataAssert(t, e, action.name, old, published.MustRelation(old.Address()))
					if e.storage.Relations[old.Address()] != stores[old.Address()] {
						t.Fatal("metadata-only publication replaced physical storage", old.Name())
					}
				}
				if !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) {
					t.Fatal("metadata-only publication rewrote physical row values")
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestInheritanceMetadataAdmissionFences(t *testing.T) {
	for _, action := range inheritanceMetadataActions {
		for _, sticky := range []bool{false, true} {
			for _, fence := range []string{"new_descendant_owner", "cancellation", "subsequent_replacement"} {
				growth := map[bool]string{false: "first_grandchild", true: "further_sticky_growth"}[sticky]
				t.Run(action.name+"/"+growth+"/"+fence, func(t *testing.T) {
					e, _, child := inheritanceMetadataFixture(t, "metadata_owner")
					if sticky {
						inheritanceMetadataChild(t, e, child, "initial_grandchild", 30, child.Owner)
						child = e.CatalogRead().MustRelation(child.Address())
					}
					holder := inheritanceAlterAXHolder(t, e, child)
					command := inheritanceMetadataScope(t, e, "metadata_owner", action.sql)
					result, joined := inheritanceMetadataWorker(t, command, holder, child)
					owner := child.Owner
					if fence == "new_descendant_owner" {
						owner = e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("metadata_other")
					}
					inheritanceMetadataChild(t, e, child, "late_grandchild", 40, owner)
					if fence == "subsequent_replacement" {
						current := e.CatalogRead().MustRelation(child.Address())
						next := *current
						next.ObjectRevision++
						next.Comment = "genuine subsequent descriptor publication"
						stage := e.BeginCatalogStage(e.CatalogRead())
						if err := stage.Replace(&next); err != nil {
							t.Fatal(err)
						}
						queryCreatePublish(t, e, stage)
						if inheritanceMarkerPublication(child, e.CatalogRead().MustRelation(child.Address())) {
							t.Fatal("actual later mutation accidentally has first-marker authority")
						}
					}
					stable := e.CatalogRead()
					stores, rows := maps.Clone(e.storage.Relations), inheritanceAlterRowRoots(e)
					if fence == "cancellation" {
						command.cancel()
					} else {
						holder.Finish(nil)
					}
					err := inheritanceAlterJoin(t, result, joined)
					switch fence {
					case "new_descendant_owner":
						state(t, err, "42501")
					case "cancellation":
						if !errors.Is(err, context.Canceled) {
							t.Fatal("canceled metadata admission did not preserve cancellation", err)
						}
					case "subsequent_replacement":
						if !errors.Is(err, ids.ErrCatalogReplaced) {
							t.Fatal("genuine replacement bypassed the exact marker-only fence", err)
						}
					}
					command.scope.Finish(err)
					holder.Finish(nil)
					if e.CatalogRead().State() != stable.State() || !reflect.DeepEqual(stores, e.storage.Relations) || !reflect.DeepEqual(rows, inheritanceAlterRowRoots(e)) {
						t.Fatal("failed metadata admission published any partial graph or row change")
					}
					lockEmpty(t, e)
				})
			}
		}
	}
}
