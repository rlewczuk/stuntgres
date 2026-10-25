package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func extensionDropUnitStatement(name string) *parser.DropExtension {
	return &parser.DropExtension{Names: []parser.Identifier{{Text: name}}, Mode: parser.DropCascade}
}

func TestExtensionDropManifest(t *testing.T) {
	e, ext, base := intarrayUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	members := slices.Clone(ext.Members)
	roots := []ids.ObjectAddress{ext.Address(), ext.Address(), base.Address()}
	original := slices.Clone(roots)
	expanded := extension.RemovalRoots(e.CatalogRead(), roots)
	if !slices.Equal(roots, original) || !slices.Equal(ext.Members, members) {
		t.Fatal("published manifest or caller input mutated")
	}
	seen := map[ids.ObjectAddress]bool{}
	for _, a := range expanded {
		if seen[a] {
			t.Fatal("duplicate retirement root", a)
		}
		seen[a] = true
	}
	for _, a := range members {
		if !seen[a] {
			t.Fatal("authentic member omitted", a)
		}
	}
	array, ok := e.CatalogRead().LookupArray(base.SQLType)
	if !ok {
		t.Fatal("missing authentic array alias")
	}
	alias := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}
	if err := extensionUnitExecute(t, e, op, extensionDropUnitStatement("intarray")); err != nil {
		t.Fatal(err)
	}
	for _, a := range append(expanded, alias) {
		if e.CatalogRead().Get(a) != nil {
			t.Fatal("committed manifest or automatic alias survived", a)
		}
	}
	if !slices.Equal(ext.Members, members) {
		t.Fatal("retained descriptor mutated by retirement")
	}
}

func TestExtensionDropRollbackIdentity(t *testing.T) {
	for _, savepoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "savepoint"}[savepoint], func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			before := e.CatalogRead()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			bound := node.(plan.InstalledTextExpr)
			tx, err := e.Begin(op, session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Rollback(op, tx)
			point := &parser.SavepointControl{Name: parser.Identifier{Text: "keep_provider"}}
			if savepoint {
				if err := extensionUnitExecute(t, e, op, point); err != nil {
					t.Fatal(err)
				}
			}
			if err := extensionUnitExecute(t, e, op, extensionDropUnitStatement("unaccent")); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
			e.Mu.Unlock()
			for _, a := range extension.RemovalRoots(before, []ids.ObjectAddress{ext.Address()}) {
				if private.Get(a) != nil || e.CatalogRead().Get(a) != before.Get(a) {
					t.Fatal("private retirement leaked", a)
				}
			}
			if savepoint {
				point.Kind = parser.RollbackSavepoint
				err = extensionUnitExecute(t, e, op, point)
			} else {
				_, err = e.Rollback(op, tx)
			}
			if err != nil {
				t.Fatal(err)
			}
			restored := e.CatalogRead()
			if savepoint {
				e.Mu.Lock()
				restored = e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
				e.Mu.Unlock()
			}
			for _, a := range extension.RemovalRoots(before, []ids.ObjectAddress{ext.Address()}) {
				if restored.Get(a) != before.Get(a) {
					t.Fatal("rollback reconstructed provider identity", a)
				}
			}
			if restored.Get(bound.Procedure.Address()) != bound.Procedure || restored.Get(bound.Dictionary.Address()) != bound.Dictionary || restored.Get(bound.Template.Address()) != bound.Template {
				t.Fatal("bound callable no longer authentic")
			}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			text, _ := value.Text()
			if err != nil || text != "Hotel" {
				t.Fatal("retained callable", text, err)
			}
		})
	}
}

func TestExtensionDropPublication(t *testing.T) {
	for _, outer := range []bool{false, true} {
		for _, consumer := range []string{"view", "query_int_column", "query_int_array_column_unsupported", "domain_check"} {
			t.Run(map[bool]string{false: "validated_publication", true: "outer_commit"}[outer]+"/"+consumer, func(t *testing.T) {
				e, op, ext, target := extensionDropUnitProvider(t, consumer)
				peer, done := roleOperation(t, e, "postgres")
				defer done()
				// A detached validated publication can encounter a newly published
				// consumer. A real transactional retirement instead holds provider
				// locks: a late checked view must wait, not publish ahead of COMMIT.
				if outer {
					tx, err := e.Begin(op, session.TxOptions{})
					if err != nil {
						t.Fatal(err)
					}
					defer e.Rollback(op, tx)
					if err := extensionUnitExecute(t, e, op, extensionDropUnitStatement(ext.Name())); err != nil {
						t.Fatal(err)
					}
					e.Mu.Lock()
					observations := session.TxOwnerOf(tx).Catalog.Dependencies
					e.Mu.Unlock()
					extensionDropUnitCaptured(t, observations, target)
					if consumer == "query_int_array_column_unsupported" {
						extensionDropUnitArrayBoundary(t, e, peer, ext, target)
						before := e.CatalogRead()
						if _, err := e.Rollback(op, tx); err != nil {
							t.Fatal(err)
						}
						extensionDropUnitPreserved(t, e, before, ext, before.Get(target))
						return
					}
					if consumer == "view" {
						extensionDropUnitWaitingView(t, e, op, tx, ext, target)
						return
					}
					owner := extensionDropUnitConsumer(t, e, peer, consumer, target)
					before := e.CatalogRead()
					unchanged := extensionDropUnitStorage(t, e)
					_, err = e.Commit(op, tx)
					extensionDropUnitRejected(t, e, before, ext, owner, err)
					unchanged()
					return
				}
				stage := extensionDropUnitRetirement(t, e, ext)
				defer e.DiscardCatalogStage(stage)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				extensionDropUnitCaptured(t, stage.Sequence.IdentityDependencyList(), target)
				if consumer == "query_int_array_column_unsupported" {
					extensionDropUnitArrayBoundary(t, e, peer, ext, target)
					return
				}
				owner := extensionDropUnitConsumer(t, e, peer, consumer, target)
				before := e.CatalogRead()
				unchanged := extensionDropUnitStorage(t, e)
				_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
				extensionDropUnitRejected(t, e, before, ext, owner, err)
				unchanged()
			})
		}
	}
}

func extensionDropUnitProvider(t *testing.T, consumer string) (*core.Engine, *session.Operation, *catalog.Extension, ids.ObjectAddress) {
	t.Helper()
	if consumer == "query_int_column" || consumer == "query_int_array_column_unsupported" {
		e, ext, base := intarrayUnitInstalled(t)
		op, done := roleOperation(t, e, "postgres")
		t.Cleanup(done)
		domainUnitTable(t, e, op, "CREATE TABLE ext_drop_sentinel(v integer PRIMARY KEY)")
		target := base.Address()
		if consumer == "query_int_array_column_unsupported" {
			array, ok := e.CatalogRead().LookupArray(base.SQLType)
			if !ok {
				t.Fatal("missing authentic automatic array")
			}
			target = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}
		}
		return e, op, ext, target
	}
	e, op, ext := extensionUnitInstalled(t)
	domainUnitTable(t, e, op, "CREATE TABLE ext_drop_sentinel(v integer PRIMARY KEY)")
	for _, address := range ext.Members {
		if p, ok := e.CatalogRead().Get(address).(*catalog.Procedure); ok && p.Name() == "unaccent" && p.Signature == catalog.UnaccentTextSignature {
			return e, op, ext, address
		}
	}
	t.Fatal("missing authentic unaccent(text)")
	return nil, nil, nil, ids.ObjectAddress{}
}

func extensionDropUnitRetirement(t *testing.T, e *core.Engine, ext *catalog.Extension) *core.CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	roots := extension.RemovalRoots(stage.Base, []ids.ObjectAddress{ext.Address()})
	for _, address := range roots {
		if err := core.StageRemoveObject(stage, e.Catalog.Domain.Ref(stage.Base.Epoch(), address), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.StageExpressionDependencies(context.Background(), stage, roots, parser.DropCascade, nil, nil, nil, ext.Address()); err != nil {
		t.Fatal(err)
	}
	return stage
}

func extensionDropUnitCaptured(t *testing.T, observations []session.DependencyObservation, target ids.ObjectAddress) {
	t.Helper()
	for _, observation := range observations {
		if slices.Contains(observation.Targets, target) {
			return
		}
	}
	t.Fatal("authentic provider omitted from retirement observation", target)
}

func extensionDropUnitArrayBoundary(t *testing.T, e *core.Engine, op *session.Operation, ext *catalog.Extension, target ids.ObjectAddress) {
	t.Helper()
	before := e.CatalogRead()
	alias := before.MustType(target)
	unchanged := extensionDropUnitStorage(t, e)
	raw := statement(t, "CREATE TABLE ext_drop_late(v query_int[])").(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var stage *core.CatalogStage
	if err == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		err = tablecmds.SchemaTableLocked(e, op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if stage != nil {
		e.DiscardCatalogStage(stage)
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "unsupported column type" {
		t.Fatalf("native query_int[] admission boundary: got %T %v", err, err)
	}
	extensionDropUnitPreserved(t, e, before, ext, alias)
	unchanged()
}

func extensionDropUnitConsumer(t *testing.T, e *core.Engine, op *session.Operation, consumer string, target ids.ObjectAddress) catalog.Descriptor {
	t.Helper()
	if consumer == "domain_check" {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN ext_drop_late AS text CONSTRAINT ext_drop_late_check CHECK (unaccent(VALUE)=VALUE)"))
		d := domainUnitType(t, e.CatalogRead(), "ext_drop_late")
		if len(d.DomainChecks) != 1 {
			t.Fatal("missing bound domain CHECK")
		}
		for _, dependency := range d.DomainChecks[0].Dependencies {
			if dependency.Ref.Address == target {
				if e.CatalogRead().Get(target) == nil {
					t.Fatal("domain CHECK provider absent")
				}
				return d
			}
		}
		t.Fatal("domain CHECK did not bind authentic installed procedure")
	}
	if consumer == "view" {
		extensionDropUnitLateView(t, e, op)
		addresses := e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ext_drop_late"})
		if len(addresses) != 1 || !view.ViewDepends(e.CatalogRead().MustRelation(addresses[0]), target) {
			t.Fatal("view did not bind authentic installed procedure")
		}
		return e.CatalogRead().Get(addresses[0])
	}
	r := domainUnitTable(t, e, op, "CREATE TABLE ext_drop_late(v query_int)")
	provider := e.CatalogRead().MustType(target)
	if len(r.Columns) != 1 || r.Columns[0].Usage.Type != provider.SQLType || !typedef.UsageDependsOn(r.Columns[0].Usage, target) {
		t.Fatal("column did not bind authentic installed type", target)
	}
	return r
}

func extensionDropUnitRejected(t *testing.T, e *core.Engine, before catalog.Read, ext *catalog.Extension, owner catalog.Descriptor, err error) {
	t.Helper()
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Errorf("late consumer must cause catalog.Conflict, got %T: %v", err, err)
	}
	extensionDropUnitPreserved(t, e, before, ext, owner)
}

func extensionDropUnitPreserved(t *testing.T, e *core.Engine, before catalog.Read, ext *catalog.Extension, owner catalog.Descriptor) {
	t.Helper()
	after := e.CatalogRead()
	if after.State() != before.State() || after.Epoch() != before.Epoch() {
		t.Error("failed retirement replaced committed root or epoch")
	}
	for address, descriptor := range before.Descriptors() {
		if after.Get(address) != descriptor {
			t.Error("failed retirement changed committed descriptor", address)
		}
	}
	if after.Get(ext.Address()) != ext || after.Get(owner.Address()) != owner {
		t.Error("failed retirement removed provider or peer consumer")
	}
}

func extensionDropUnitStorage(t *testing.T, e *core.Engine) func() {
	t.Helper()
	relations := maps.Clone(e.Storage.Relations)
	shapes := make(map[ids.ObjectAddress]session.StorageChange, len(relations))
	for address, store := range relations {
		if store != nil {
			shapes[address] = session.StorageChange{Original: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation}
		}
	}
	indexes := maps.Clone(e.Storage.IndexRoots)
	return func() {
		t.Helper()
		if !maps.Equal(relations, e.Storage.Relations) || !maps.Equal(indexes, e.Storage.IndexRoots) {
			t.Error("failed retirement changed committed storage or index roots")
		}
		for address, shape := range shapes {
			store := e.Storage.Relations[address]
			if store != shape.Original || store.Generation != shape.Generation || store.Ends != shape.Ends || !storage.SameVersionRoot(store.Versions, shape.Root) {
				t.Error("failed retirement changed committed row root", address)
			}
		}
	}
}

func TestExtensionDropPublicationCandidate(t *testing.T) {
	for _, consumer := range []string{"query_int_column", "domain_check"} {
		for _, control := range []string{"absent", "rewired", "still_dependent", "provider_retained", "unrelated", "stale_original"} {
			t.Run(consumer+"/"+control, func(t *testing.T) {
				e, op, ext, target := extensionDropUnitProvider(t, consumer)
				stage := extensionDropUnitRetirement(t, e, ext)
				defer e.DiscardCatalogStage(stage)
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				extensionDropUnitCaptured(t, stage.Sequence.IdentityDependencyList(), target)
				if control == "unrelated" {
					domainUnitTable(t, e, op, "CREATE TABLE ext_drop_unrelated(v integer)")
					if _, err := e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
						t.Fatal("unrelated peer publication rejected", err)
					}
					return
				}
				owner := extensionDropUnitConsumer(t, e, op, consumer, target)
				current := e.CatalogRead()
				candidate := catalog.CloneState(current.State())
				for _, observation := range stage.Sequence.IdentityDependencyList() {
					for _, address := range observation.Targets {
						candidate.RemoveDescriptor(address)
					}
				}
				switch control {
				case "absent":
					candidate.RemoveDescriptor(owner.Address())
				case "rewired", "still_dependent":
					switch d := owner.(type) {
					case *catalog.Relation:
						replacement := *d
						replacement.Columns = slices.Clone(d.Columns)
						replacement.ObjectRevision++
						if control == "rewired" {
							integer, _ := current.LookupType(sqltype.Int4)
							replacement.Columns[0].Usage = integer.Usage()
						}
						candidate.SetDescriptor(d.Address(), &replacement)
					case *catalog.Type:
						if control == "rewired" {
							rewire := e.BeginCatalogStage(current)
							defer e.DiscardCatalogStage(rewire)
							if err := (tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{CatalogStage: rewire}}}).RemoveDomainCheck(d, d.DomainChecks[0].OID); err != nil {
								t.Fatal(err)
							}
							candidate.SetDescriptor(d.Address(), rewire.Get(d.Address()))
						} else {
							replacement := *d
							replacement.ObjectRevision++
							candidate.SetDescriptor(d.Address(), &replacement)
						}
					}
				case "provider_retained":
					candidate.SetDescriptor(target, current.Get(target))
				case "stale_original":
					// Predicate acceptance never bypasses the real predecessor fence.
					stale := e.BeginCatalogStage(current)
					defer e.DiscardCatalogStage(stale)
					changed := e.BeginCatalogStage(current)
					defer e.DiscardCatalogStage(changed)
					switch d := owner.(type) {
					case *catalog.Relation:
						first, second := *d, *d
						first.ObjectRevision++
						second.ObjectRevision++
						if err = stale.Replace(&first); err == nil {
							err = changed.Replace(&second)
						}
					case *catalog.Type:
						first, second := *d, *d
						first.ObjectRevision++
						second.ObjectRevision++
						if err = stale.Replace(&first); err == nil {
							err = changed.Replace(&second)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					stalePrepared, err := e.ValidateCatalogStage(context.Background(), stale)
					if err != nil {
						t.Fatal(err)
					}
					domainUnitPublish(t, e, changed)
					before := e.CatalogRead()
					_, err = publishCatalog(e, stalePrepared, core.PublicationToken{Epoch: stale.Base.Epoch()})
					extensionDropUnitRejected(t, e, before, ext, before.Get(owner.Address()), err)
					return
				}
				err = core.CatalogDependencyPreflight(current.State(), candidate, stage.Sequence.IdentityDependencyList(), nil)
				var conflict catalog.Conflict
				if control == "still_dependent" {
					if !errors.As(err, &conflict) {
						t.Fatalf("surviving candidate dependency: got %T %v", err, err)
					}
				} else if err != nil {
					t.Fatal("non-surviving candidate dependency rejected", err)
				}
				if e.CatalogRead().State() != current.State() || current.Get(owner.Address()) != owner {
					t.Fatal("candidate inspection mutated committed source")
				}
				if r, ok := owner.(*catalog.Relation); ok && !typedef.UsageDependsOn(r.Columns[0].Usage, target) {
					t.Fatal("rewire mutated original column")
				}
				if d, ok := owner.(*catalog.Type); ok && len(d.DomainChecks) != 1 {
					t.Fatal("rewire mutated original domain CHECK")
				}
			})
		}
	}
	t.Run("automatic_array_usage", func(t *testing.T) {
		e, _, ext, target := extensionDropUnitProvider(t, "query_int_array_column_unsupported")
		before := e.CatalogRead()
		alias := before.MustType(target)
		base := before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(alias.SQLType.Element().ID())})
		array, ok := before.LookupArray(base.SQLType)
		if !ok || array != alias.SQLType || before.Get(base.Address()) != base || before.Get(ext.Address()) != ext {
			t.Fatal("automatic alias did not retain authentic installation identity")
		}
		if !typedef.UsageDependsOn(alias.SQLType.Usage(), target) || typedef.UsageDependsOn(alias.SQLType.Usage(), base.Address()) || typedef.UsageDependsOn(alias.SQLType.Usage(), ext.Address()) {
			t.Fatal("type usage must match exact automatic alias, not its base or unrelated owner")
		}
		stage := extensionDropUnitRetirement(t, e, ext)
		defer e.DiscardCatalogStage(stage)
		extensionDropUnitCaptured(t, stage.Sequence.IdentityDependencyList(), target)
		if before.Get(target) != alias || before.MustType(target).SQLType != array {
			t.Fatal("retirement mutated retained automatic alias")
		}
	})
}

func extensionDropUnitViewStatement() *parser.CreateView {
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	return &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ext_drop_late"}}}, Query: &parser.Select{Targets: []parser.Expr{call}, Labels: []parser.Identifier{{Text: "v"}}}}
}

func extensionDropUnitLateView(t *testing.T, e *core.Engine, op *session.Operation) {
	t.Helper()
	if err := extensionUnitExecute(t, e, op, extensionDropUnitViewStatement()); err != nil {
		t.Fatal(err)
	}
}

func extensionDropUnitWaitingView(t *testing.T, e *core.Engine, dropper *session.Operation, tx ids.TxRef, ext *catalog.Extension, target ids.ObjectAddress) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	creator, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	result := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("late view creator did not join")
			}
		}
	}()
	stmt := extensionDropUnitViewStatement()
	go func() { result <- extensionUnitExecute(t, e, creator, stmt) }()
	lockPending(t, e, 1)
	before := e.CatalogRead()
	resource := lock.Resource{ObjectRef: before.Domain().Ref(before.Epoch(), target)}
	entry := e.Locks.Inspect().Resources[resource]
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != lock.AccessShare || entry.Holders[session.TxOwnerOf(tx).Ticket].Mode&lock.AccessExclusive == 0 {
		t.Fatal("late checked view did not wait on exact retiring member", target)
	}
	unchanged := extensionDropUnitStorage(t, e)
	if _, err := e.Commit(dropper, tx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("late view creator did not finish after retirement")
	}
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: "referenced function was concurrently dropped"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("retired view provider diagnostic: got %v, want %#v", err, want)
	}
	after := e.CatalogRead()
	for _, address := range extension.RemovalRoots(before, []ids.ObjectAddress{ext.Address()}) {
		if after.Get(address) != nil {
			t.Error("committed extension member survived", address)
		}
	}
	if address := after.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ext_drop_late"}); address != (ids.ObjectAddress{}) {
		t.Error("late view published against retired provider", address)
	}
	unchanged()
}

func TestExtensionDropDiagnostics(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	if err := extensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "fuzzystrmatch"}}); err != nil {
		t.Fatal(err)
	}
	args := []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "kitten"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "sitting"}},
	}
	for _, n := range []int64{1, 1, 1, 3} {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}})
	}
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "levenshtein_less_equal"}}}, Args: args}
	consumer := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ext_drop_generic"}}}, Query: &parser.Select{Targets: []parser.Expr{call}, Labels: []parser.Identifier{{Text: "distance"}}}}
	if err := extensionUnitExecute(t, e, op, consumer); err != nil {
		t.Fatal(err)
	}
	before := e.Catalog.State
	stmt := extensionDropUnitStatement("fuzzystrmatch")
	stmt.Mode = parser.DropRestrict
	err := extensionUnitExecute(t, e, op, stmt)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Detail != "view ext_drop_generic depends on function levenshtein_less_equal(text,text,integer,integer,integer,integer)" {
		t.Fatal("generic stored consumer must reject retirement without renderer panic", err)
	}
	if e.Catalog.State != before {
		t.Fatal("failed generic retirement mutated committed identities")
	}
	stmt.Mode = parser.DropCascade
	if err := extensionUnitExecute(t, e, op, stmt); err != nil {
		t.Fatal(err)
	}
	for a, d := range before.Descriptors() {
		if _, ok := d.(*catalog.Extension); ok || catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) || d.Name() == "ext_drop_generic" {
			if e.CatalogRead().Get(a) != nil {
				t.Fatal("generic dependency closure survived cascade", a)
			}
		}
	}
}
