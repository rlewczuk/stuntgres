package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func alterGenerationPopulate(t *testing.T, e *Engine, op *Operation) {
	t.Helper()
	insert := &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "g"}}}, Targets: []parser.InsertTarget{{Name: "a"}}, Rows: [][]parser.Expr{{alterGenerationLiteral(1)}, {alterGenerationLiteral(2)}}}
	if _, err := e.Execute(op, insert, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func alterGenerationReplacement() parser.AlterTableAction {
	return alterGenerationAction(parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "+"}}}, Args: []parser.Expr{parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}}, alterGenerationLiteral(2)}})
}

func TestAlterGeneratedExpressionPublication(t *testing.T) {
	for _, drop := range []bool{false, true} {
		for _, veto := range []bool{false, true} {
			t.Run(map[bool]string{false: "set", true: "drop"}[drop]+"-"+map[bool]string{false: "commit", true: "cancel"}[veto], func(t *testing.T) {
				e, admin, _, ref := generatedAuthorizationFixture(t)
				alterGenerationPopulate(t, e, admin)
				old := e.catalog.state.MustRelation(ref.Address)
				store := e.storage.relations[ref.Address]
				snapshot := e.catalog.state
				ends, generation := store.ends, store.generation
				rowID, versionID, frontier := e.storage.nextRow, e.storage.nextVersion, e.commitSeq
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, cleanup, err := testOperation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer cleanup()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished unit"))
				action := alterGenerationReplacement()
				if drop {
					action = parser.AlterTableAction{Action: parser.DropExpression, Column: parser.Identifier{Text: "b"}}
				}
				if _, err := scope.Execute(alterGenerationStatement(action), 0, nil); err != nil {
					t.Fatal(err)
				}
				candidate := scope.catalog.descriptors[ref.Address].(*catalog.Relation)
				if candidate == old || candidate.Revision() != old.Revision()+1 || e.catalog.state != snapshot || e.storage.relations[ref.Address] != store {
					t.Fatal("stage published before Finish")
				}
				shape := scope.catalog.storage[ref.Address]
				if drop {
					if len(candidate.ColumnMeta.Generations) != 0 || shape.replacement != store {
						t.Fatal("DROP rewrote source storage")
					}
				} else {
					if shape.replacement == store || candidate.ColumnMeta.Generations[2].OID == old.ColumnMeta.Generations[2].OID {
						t.Fatal("SET lacked detached rewrite/definition")
					}
					for i, r := range shape.replacement.versions {
						n, err := r.values[1].Integer()
						if err != nil || n != int64(i+3) {
							t.Fatal(n, err)
						}
					}
				}
				if !veto {
					if err := scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					if e.catalog.state.Get(ref.Address) != candidate || scope.owner.catalog != nil || snapshot.Get(ref.Address) != old || old.ColumnMeta.Generations[2].Kind != catalog.GenerationStored {
						t.Fatal("publication changed retained source/failed install")
					}
					for i, row := range store.versions {
						n, err := row.values[1].Integer()
						if err != nil || n != int64(i+2) {
							t.Fatal("retained rows changed", n, err)
						}
					}
					for i, row := range e.storage.relations[ref.Address].versions {
						n, err := row.values[1].Integer()
						offset := 2
						if !drop {
							offset = 3
						}
						if err != nil || n != int64(i+offset) {
							t.Fatal("installed values wrong", n, err)
						}
					}
					lockEmpty(t, e)
					return
				}
				cancel()
				if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
					t.Fatal("Finish cancellation veto", err)
				}
				if e.catalog.state != snapshot || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != store || store.ends != ends || store.generation != generation || e.storage.nextRow != rowID || e.storage.nextVersion != versionID || e.commitSeq != frontier || scope.owner.catalog != nil {
					t.Fatal("Finish veto changed installed roots/counters/owner")
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestAlterGeneratedExpressionProviderFences(t *testing.T) {
	for _, kind := range []string{"cancel", "reset", "restore", "acl", "principal", "descriptor", "role"} {
		t.Run(kind, func(t *testing.T) {
			e, admin, actor, ref := generatedAuthorizationFixture(t)
			alterGenerationPopulate(t, e, admin)
			old := e.catalog.state.MustRelation(ref.Address)
			store := e.storage.relations[ref.Address]
			retained := e.CatalogRead()
			rowID, versionID, frontier := e.storage.nextRow, e.storage.nextVersion, e.commitSeq
			generation, ends := store.generation, store.ends
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, cleanup, err := testOperation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			provider := e.catalog.Providers[ids.RelationClassID]
			defer func() { e.catalog.Providers[ids.RelationClassID] = provider }()
			calls := 0
			e.catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				calls++
				e.catalog.Providers[ids.RelationClassID] = provider
				if !e.mu.TryLock() {
					t.Error("generation callback held Engine.mu")
				} else {
					e.mu.Unlock()
				}
				if e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != store {
					t.Error("candidate leaked before provider validation")
				}
				switch kind {
				case "cancel":
					cancel()
				case "reset":
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := e.Restore(context.Background(), strings.NewReader("")); err != nil {
						t.Fatal(err)
					}
				case "acl":
					if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: admin.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: actor.RoleState().CurrentUser}, Privileges: authorization.SelectPrivilege}}}); err != nil {
						t.Fatal(err)
					}
				case "principal":
					op.session.mu.Lock()
					op.session.authorization.revision++
					op.session.mu.Unlock()
				case "role":
					if _, err := e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "generation_changed_role"}}, 0, nil); err != nil {
						t.Fatal(err)
					}
				case "descriptor":
					changed := *old
					changed.ObjectRevision++
					changed.Comment = "external generation provider change"
					stage := e.BeginCatalogStage(e.CatalogRead())
					if err := stage.Replace(&changed); err != nil {
						t.Fatal(err)
					}
					validated, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := e.PublishCatalog(validated, PublicationToken{Epoch: ref.Epoch}); err != nil {
						t.Fatal(err)
					}
				}
			}}
			_, err = e.Execute(op, alterGenerationStatement(alterGenerationReplacement()), 0, nil)
			if err == nil || calls != 1 {
				t.Fatal("provider fence accepted stale generation", kind, calls, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind == "descriptor" || kind == "acl" {
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal("provider conflict changed error boundary", err)
				}
			}
			if kind == "reset" || kind == "restore" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			} else {
				if e.storage.relations[ref.Address] != store || store.generation != generation || store.ends != ends || e.storage.nextRow != rowID || e.storage.nextVersion != versionID {
					t.Fatal("provider veto changed storage/counters", err)
				}
				current := e.catalog.state.MustRelation(ref.Address)
				if !reflect.DeepEqual(current.ColumnMeta.Generations, old.ColumnMeta.Generations) || current.ColumnMeta.Generations[2].OID != old.ColumnMeta.Generations[2].OID {
					t.Fatal("failed generation replaced old recipe")
				}
				if kind == "cancel" && e.commitSeq != frontier {
					t.Fatal("canceled generation advanced commit frontier")
				}
			}
			if retained.Get(ref.Address) != old || len(old.ColumnMeta.Generations) != 1 || len(store.versions) != 2 {
				t.Fatal("retained snapshot changed")
			}
			lockEmpty(t, e)
		})
	}
}

func TestAlterGeneratedExpressionDeniedEvaluation(t *testing.T) {
	e, admin, actor, ref := generatedAuthorizationFixture(t)
	generatedAuthorizationGrant(t, e, admin, actor, ref, 0, authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege|authorization.ReferencesPrivilege|authorization.TriggerPrivilege)
	before := e.catalog.state.Get(ref.Address)
	// Cooking this constant division would fail22012 if denied admission evaluated it.
	expr := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "/"}}}, Args: []parser.Expr{alterGenerationLiteral(1), alterGenerationLiteral(0)}}
	_, err := e.Execute(actor, alterGenerationStatement(alterGenerationAction(expr)), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "must be owner of table g" || e.catalog.state.Get(ref.Address) != before {
		t.Fatal("denied generation cooked/published", err)
	}
	lockEmpty(t, e)
}

func TestAlterGeneratedExpressionRevisionExhaustion(t *testing.T) {
	e, admin, _, ref := generatedAuthorizationFixture(t)
	old := e.catalog.state.MustRelation(ref.Address)
	limit := *old
	limit.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&limit); err != nil {
		t.Fatal(err)
	}
	validated, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(validated, PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	catalog, store, oid := e.catalog.state, e.storage.relations[ref.Address], e.catalog.nextOID
	_, err = e.Execute(admin, alterGenerationStatement(alterGenerationReplacement()), 0, nil)
	if err == nil || err.Error() != "stuntgres: definition revision exhausted" || e.catalog.state != catalog || e.storage.relations[ref.Address] != store || e.catalog.nextOID != oid {
		t.Fatal("revision overflow changed state", err)
	}
	lockEmpty(t, e)
}
