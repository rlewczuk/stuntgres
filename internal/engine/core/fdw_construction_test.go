package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func foreignConstructionUnitScope(t *testing.T) (*core.Engine, *core.StatementScope, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE construction_audit(v integer)",
		"CREATE FUNCTION construction_validator(text[],oid) RETURNS integer LANGUAGE SQL AS $$INSERT INTO construction_audit VALUES(7); SELECT 7$$",
		"CREATE FOREIGN DATA WRAPPER construction_wrapper",
		"CREATE SERVER construction_server FOREIGN DATA WRAPPER construction_wrapper",
		"ALTER FOREIGN DATA WRAPPER construction_wrapper VALIDATOR construction_validator",
	} {
		enginetest.SessionSQL(t, session, q)
	}
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			scope.Finish(context.Canceled)
		}
	})
	enclosing := enginetest.Statement(t, "SELECT 1")
	description, err := scope.Describe(enclosing)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &description
	result, err := scope.Execute(enclosing, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := make([]sqltype.Value, len(result.Rows.Schema()))
	if ok, err := result.Rows.Next(op.Context(), row); err != nil || !ok {
		t.Fatal("enclosing first row", ok, err)
	}
	if ok, err := result.Rows.Next(op.Context(), row); err != nil || ok {
		t.Fatal("enclosing EOF", ok, err)
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	core.ScopeForeignUnlocked(scope)
	read := e.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	audit := read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "construction_audit"})
	return e, scope, audit
}

func foreignConstructionUnitStage(t *testing.T, e *core.Engine, scope *core.StatementScope, name string) (*core.CatalogStage, []fdw.Validation, *catalog.Relation) {
	t.Helper()
	statement := enginetest.Statement(t, "CREATE FOREIGN TABLE "+name+"(id integer OPTIONS(column_name 'id'),label text) SERVER construction_server OPTIONS(table_name 'data')").(*parser.ForeignDDL)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	jobs, _, err := fdw.StageTable(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, statement, scope.Input)
	var relation *catalog.Relation
	for _, descriptor := range stage.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.RelationKind == catalog.ForeignRelation && d.Name() == name {
			relation = d
		}
	}
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if err != nil || relation == nil || len(jobs) != 2 || jobs[0].Attribute != 0 || jobs[1].Attribute != relation.Attributes[0] {
		t.Fatal("construction stage or stable schedule", relation, jobs, err)
	}
	return stage, jobs, relation
}

func foreignConstructionUnitRead(e *core.Engine, scope *core.StatementScope) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(scope.Catalog)
}

func foreignConstructionUnitComplete(t *testing.T, scope *core.StatementScope, token *core.ForeignConstruction, jobs []fdw.Validation) {
	t.Helper()
	for i := range jobs {
		var options []catalog.ForeignOption
		if jobs[i].Attribute != 0 {
			prepared, values, err := fdw.PrepareColumnValidation(foreignConstructionUnitRead(scope.Engine(), scope), jobs[i])
			if err != nil {
				t.Fatal(err)
			}
			jobs[i], options = prepared, values
		}
		if err := fdw.RunValidations(scope.Op.Context(), jobs[i:i+1], scope.InvokeForeignValidator); err != nil {
			t.Fatal(err)
		}
		if err := token.Advance(jobs[i].Attribute, options); err != nil {
			t.Fatal(err)
		}
	}
	if err := token.Complete(); err != nil {
		t.Fatal(err)
	}
}

func TestFDWForeignConstruction(t *testing.T) {
	t.Run("complete_private_identity", func(t *testing.T) {
		e, scope, audit := foreignConstructionUnitScope(t)
		stage, jobs, intended := foreignConstructionUnitStage(t, e, scope, "construction_target")
		token, err := scope.StageForeignConstruction(stage, intended.Address(), []int32{intended.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		read := foreignConstructionUnitRead(e, scope)
		shell, ok := read.Relation(intended.Address())
		if !ok || shell.RelationKind != catalog.ForeignRelation || shell.Foreign != nil || shell.RowType != intended.RowType || !slices.Equal(shell.Attributes, intended.Attributes) {
			t.Fatal("incorrect immutable construction shell", shell)
		}
		if err := core.RelationProvider.Validate(read, shell); err == nil {
			t.Fatal("ordinary provider accepted incomplete foreign relation")
		}
		// A genuinely separate session reads the committed catalog, not this
		// statement's shell or its transient type/attribute identities.
		peer := enginetest.StartSession(t, e, nil)
		peerOp, err := peer.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(peerOp)
		_, peerVisible := auth.Catalog.Relation(intended.Address())
		e.Mu.Unlock()
		peerOp.Done()
		if err != nil || peerVisible || e.Storage.Relations[intended.Address()] != nil {
			t.Fatal("construction escaped private ownership", peerVisible, err)
		}
		if original, recorded := scope.Catalog.Originals[intended.Address()]; !recorded || original != nil {
			t.Fatal("committed absence fence lost")
		}
		if predecessor, recorded := scope.Catalog.Names[catalog.DescriptorNameKey(intended)]; !recorded || predecessor != (ids.ObjectAddress{}) {
			t.Fatal("committed name fence lost")
		}
		foreignConstructionUnitComplete(t, scope, token, jobs)
		complete := foreignConstructionUnitRead(e, scope).MustRelation(intended.Address())
		if complete.RowType != intended.RowType || !slices.Equal(complete.Attributes, intended.Attributes) || complete.Foreign == nil || !slices.Equal(complete.Foreign.Columns[complete.Attributes[0]], []catalog.ForeignOption{{Name: "column_name", Value: "id"}}) || shell.Foreign != nil || core.ScopePendingForeignConstructions(scope) != 0 {
			t.Fatal("completion changed identity, options, old shell or pending ownership")
		}
		if e.CatalogRead().Get(intended.Address()) != nil || len(scope.Stage.Rows[audit]) != 2 {
			t.Fatal("completion committed early or lost validator writes")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if e.CatalogRead().Get(intended.Address()) != complete || len(e.Storage.Relations[audit].Versions) != 2 {
			t.Fatal("completed metadata and validator writes did not publish together")
		}
	})
	for _, outcome := range []string{"unfinished", "skipped_column", "failure", "cancel", "mutated", "dropped"} {
		t.Run(outcome, func(t *testing.T) {
			e, scope, audit := foreignConstructionUnitScope(t)
			stage, jobs, intended := foreignConstructionUnitStage(t, e, scope, "construction_target")
			token, err := scope.StageForeignConstruction(stage, intended.Address(), []int32{intended.Attributes[0]})
			if err != nil {
				t.Fatal(err)
			}
			if err := fdw.RunValidations(scope.Op.Context(), jobs[:1], scope.InvokeForeignValidator); err != nil {
				t.Fatal(err)
			}
			var primary error
			switch outcome {
			case "skipped_column":
				if err := token.Advance(0, nil); err != nil {
					t.Fatal(err)
				}
				primary = token.Complete()
				if primary == nil || primary.Error() != "stuntgres: incomplete foreign column construction" {
					t.Fatal("scheduled column was skipped", primary)
				}
			case "failure":
				primary = errors.New("validator failed after local write")
			case "cancel":
				ctx, cancel := context.WithCancel(scope.Op.Ctx)
				cancel()
				scope.Op.Ctx = ctx
				primary = context.Canceled
			case "mutated", "dropped":
				e.Mu.Lock()
				if outcome == "dropped" {
					e.CatalogChangeLocked(scope.Catalog, intended.Address(), nil)
				} else {
					changed := *e.PrivateCatalogLocked(scope.Catalog).MustRelation(intended.Address())
					changed.ObjectRevision, err = e.Catalog.RevisionLocked()
					if err == nil {
						e.CatalogChangeLocked(scope.Catalog, intended.Address(), &changed)
					}
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				primary = token.Advance(0, nil)
				var conflict catalog.Conflict
				if !errors.As(primary, &conflict) {
					t.Fatal("callback mutation was overwritten or resurrected", primary)
				}
			}
			finish := scope.Finish(primary)
			if outcome == "unfinished" {
				if finish == nil || finish.Error() != "stuntgres: incomplete foreign construction" {
					t.Fatal("unfinished success escaped guard", finish)
				}
			} else if !errors.Is(finish, primary) {
				t.Fatal("construction changed primary failure", finish, primary)
			}
			if core.ScopePendingForeignConstructions(scope) != 0 || e.CatalogRead().Get(intended.Address()) != nil || len(e.Storage.Relations[audit].Versions) != 0 || token.Advance(0, nil) == nil {
				t.Fatal("failed construction leaked metadata, writes or token authority")
			}
		})
	}
	t.Run("nested_guard", func(t *testing.T) {
		e, scope, audit := foreignConstructionUnitScope(t)
		outerStage, _, outerRelation := foreignConstructionUnitStage(t, e, scope, "construction_outer")
		outer, err := scope.StageForeignConstruction(outerStage, outerRelation.Address(), []int32{outerRelation.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		innerStage, jobs, innerRelation := foreignConstructionUnitStage(t, e, scope, "construction_inner")
		inner, err := scope.StageForeignConstruction(innerStage, innerRelation.Address(), []int32{innerRelation.Attributes[0]})
		if err != nil {
			t.Fatal(err)
		}
		foreignConstructionUnitComplete(t, scope, inner, jobs)
		if core.ScopePendingForeignConstructions(scope) != 1 {
			t.Fatal("inner completion retired outer construction guard")
		}
		if err := scope.Finish(nil); err == nil || err.Error() != "stuntgres: incomplete foreign construction" {
			t.Fatal("nested incomplete construction published", err)
		}
		if e.CatalogRead().Get(outerRelation.Address()) != nil || e.CatalogRead().Get(innerRelation.Address()) != nil || len(e.Storage.Relations[audit].Versions) != 0 || outer.Advance(0, nil) == nil {
			t.Fatal("nested rollback leaked metadata, audit or token")
		}
	})
}
