package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func extensionTemporaryOperation(t *testing.T, e *Engine, name string) (*session.Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	var op *session.Operation
	var once sync.Once
	close := func() {
		once.Do(func() {
			if op != nil {
				op.Done()
			}
			s.RequestEnd(session.CleanClose)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.WaitEnd(ctx); err != nil {
				t.Error("physical TEMP session drain", err)
			}
		})
	}
	t.Cleanup(close)
	op, err = s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return op, close
}
func extensionTemporaryInstalled(t *testing.T) (*Engine, *session.Operation, *session.Operation, *catalog.Extension, func()) {
	t.Helper()
	e := New()
	owner, closeOwner := extensionTemporaryOperation(t, e, "postgres")
	installer, _ := extensionTemporaryOperation(t, e, "postgres")
	roleExecute(t, e, owner, "CREATE TEMP TABLE ext_seed(id INT)")
	namespace := e.Catalog.State.MustNamespace(owner.Session.TemporaryNamespace)
	if err := extensionUnitExecute(t, e, installer, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace.Name())); err != nil {
		t.Fatal(err)
	}
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	return e, owner, installer, ext, closeOwner
}
func extensionTemporaryConsumerUnits(t *testing.T, e *Engine, op *session.Operation, ext *catalog.Extension) *catalog.Relation {
	t.Helper()
	name := e.Catalog.State.Get(ext.TargetNamespace).Name()
	for _, q := range []string{
		fmt.Sprintf("CREATE TABLE public.ext_survivor(id INT,txt TEXT DEFAULT %s.unaccent('Hôtel'::text),CONSTRAINT ext_check CHECK(%s.unaccent(txt)=txt))", name, name),
		"CREATE INDEX ext_survivor_id ON public.ext_survivor(id)",
		"INSERT INTO public.ext_survivor(id) VALUES(1)",
		fmt.Sprintf("CREATE VIEW public.ext_view AS SELECT %s.unaccent('Hôtel'::text) AS v", name),
		"CREATE VIEW public.ext_chain AS SELECT v FROM public.ext_view",
	} {
		roleExecute(t, e, op, q)
	}
	return e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ext_survivor"}))
}
func extensionTemporaryStage(t *testing.T, e *Engine, ext *catalog.Extension) (*CatalogStage, *TemporarySessionIndexes) {
	t.Helper()
	base := e.CatalogRead()
	targets, err := catalog.NamespaceChildren(base, map[ids.ObjectAddress]bool{ext.TargetNamespace: true})
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(base)
	SetStageTerminalCleanup(stage, true)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	for _, target := range targets {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), target)); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	if err := StageOwnedExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	indexes, err := EnginePrepareTemporarySessionIndexes(e, stage)
	if err != nil {
		t.Fatal(err)
	}
	return stage, indexes
}
func extensionTemporaryGoneUnit(t *testing.T, e *Engine, ext *catalog.Extension) {
	t.Helper()
	namespace, ok := e.Catalog.State.Namespace(ext.TargetNamespace)
	if !ok {
		t.Fatal("physical end removed retained TEMP namespace metadata")
	}
	assertTemporaryNamespacePair(t, e, namespace.Temporary, namespace.Address())
	for _, address := range append([]ids.ObjectAddress{ext.Address()}, ext.Members[:]...) {
		if e.Catalog.State.Get(address) != nil {
			t.Fatal("retired provider identity retained", address)
		}
	}
	for key, address := range e.Catalog.State.Names() {
		if address == ext.Address() || slices.Contains(ext.Members[:], address) || key.Namespace == ext.TargetNamespace {
			t.Fatal("retired signature/name retained", key)
		}
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), e.CatalogRead(), EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges {
		if edge.Referenced == ext.Address() || edge.Dependent == ext.Address() || slices.Contains(ext.Members[:], edge.Referenced) || slices.Contains(ext.Members[:], edge.Dependent) {
			t.Fatal("dangling retirement edge", edge)
		}
	}
}
func TestExtensionTemporaryClosure(t *testing.T) {
	e, owner, installer, ext, _ := extensionTemporaryInstalled(t)
	t.Run("own_helper_atomicity", func(t *testing.T) {
		e := New()
		owner, _ := extensionTemporaryOperation(t, e, "postgres")
		roleExecute(t, e, owner, "CREATE TEMP TABLE own_seed(id INT)")
		namespace := e.Catalog.State.Get(owner.Session.TemporaryNamespace).Name()
		before := e.Catalog.State
		err := extensionUnitExecute(t, e, owner, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace))
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function unaccent_init(internal) does not exist", Context: extension.UnaccentTemplateContext}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before {
			t.Fatal("own staged helper failure did not preserve full authority", err)
		}
		if _, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}); exists {
			t.Fatal("failed helper install leaked extension")
		}
	})
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	store := e.Storage.Relations[survivor.Address()]
	oldRows, oldEnds := store.Versions, store.Ends
	index := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	oldIndex := e.Storage.IndexRoots[index]
	base := e.Catalog.State
	stage, indexes := extensionTemporaryStage(t, e, ext)
	replacement := stage.Get(survivor.Address()).(*catalog.Relation)
	if len(replacement.ColumnMeta.Defaults) != 0 || len(replacement.Constraints.Check) != 0 || len(survivor.ColumnMeta.Defaults) != 1 || len(survivor.Constraints.Check) != 1 {
		t.Fatal("candidate changed predecessor or failed expression closure")
	}
	roleExecute(t, e, installer, "CREATE VIEW public.ext_late AS SELECT v FROM public.ext_chain")
	e.Mu.Lock()
	installed, err := EngineInstallTemporarySessionStageLocked(e, owner.Session, stage, []ids.ObjectAddress{ext.TargetNamespace}, indexes)
	e.Mu.Unlock()
	if err != nil || installed {
		t.Fatal("late dependent bypassed whole-root fence", installed, err)
	}
	if e.Catalog.State.Get(survivor.Address()) != survivor || e.Catalog.State.Get(ext.Address()) != ext {
		t.Fatal("failed candidate published partially")
	}
	if err := EngineEndTemporarySession(e, owner.Session); err != nil {
		t.Fatal(err)
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.Catalog.State.MustRelation(survivor.Address())
	if current == survivor || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || e.Storage.Relations[survivor.Address()] != store || e.Storage.IndexRoots[index] != oldIndex || !storage.SameVersionRoot(oldRows, store.Versions) || oldEnds != store.Ends || e.Catalog.State.Epoch() != base.Epoch() {
		t.Fatal("surviving row/index/provenance changed")
	}
	for _, name := range []string{"ext_view", "ext_chain", "ext_late"} {
		if _, exists := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}); exists {
			t.Fatal("view closure incomplete", name)
		}
	}
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id,txt) VALUES(2,'Hôtel')")
	roleExecute(t, e, installer, "INSERT INTO public.ext_survivor(id) VALUES(3)")
}
func TestExtensionTemporaryCleanup(t *testing.T) {
	t.Run("preparation_stale", func(t *testing.T) { extensionTemporaryStaleEnd(t, false) })
	t.Run("postdrain_late_consumer", func(t *testing.T) { extensionTemporaryStaleEnd(t, true) })
	t.Run("postdrain_saturated_default_check", extensionTemporarySaturatedEnd)
	t.Run("validation_structural_failure_ordinary", func(t *testing.T) { extensionTemporaryStructuralEnd(t, false) })
	t.Run("validation_structural_failure_postdrain", func(t *testing.T) { extensionTemporaryStructuralEnd(t, true) })
	t.Run("mixed_collation", func(t *testing.T) {
		e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
		extensionTemporaryConsumerUnits(t, e, installer, ext)
		roleExecute(t, e, owner, `CREATE COLLATION pg_temp.ext_mixed FROM pg_catalog."C"`)
		roleExecute(t, e, owner, "CREATE TABLE public.ext_mixed_survivor(id INT,txt TEXT COLLATE pg_temp.ext_mixed)")
		roleExecute(t, e, owner, "INSERT INTO public.ext_mixed_survivor VALUES(9,'é')")
		closeOwner()
		extensionTemporaryGoneUnit(t, e, ext)
		for _, d := range e.Catalog.State.Descriptors() {
			if d.Namespace() == ext.TargetNamespace {
				t.Fatal("mixed provider retirement left a child", d.Address())
			}
		}
	})
	t.Run("private_authority", func(t *testing.T) {
		e, _, _, ext, _ := extensionTemporaryInstalled(t)
		private, err := EngineStartSession(e, context.Background(), session.SessionStart{Authenticated: e.RoleStore.Domain.Principal(10)}, true)
		if err != nil {
			t.Fatal(err)
		}
		var op *session.Operation
		var once sync.Once
		closePrivate := func() {
			once.Do(func() {
				if op != nil {
					op.Done()
				}
				private.RequestEnd(session.CleanClose)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := private.WaitEnd(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(closePrivate)
		op, err = private.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		before := e.Catalog.State
		_, err = e.Execute(op, statement(t, "CREATE TEMP TABLE private_extension_seed(id INT)"), 0, nil)
		if err == nil || e.Catalog.State != before {
			t.Fatal("private maintenance acquired public TEMP authority", err)
		}
		if err := EngineEndTemporarySession(e, private); err != nil || e.Catalog.State != before || e.Catalog.State.Get(ext.Address()) != ext {
			t.Fatal("private retirement borrowed a public token", err)
		}
		closePrivate()
	})
	t.Run("physical_and_old_rows", func(t *testing.T) {
		e, owner, installer, ext, closeOwner := extensionTemporaryInstalled(t)
		survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
		held := roleExecute(t, e, installer, "SELECT txt FROM public.ext_survivor")
		peer, _ := extensionTemporaryOperation(t, e, "postgres")
		roleExecute(t, e, peer, "CREATE TEMP TABLE peer_survives(id INT)")
		peerNamespace := e.Catalog.State.Get(peer.Session.TemporaryNamespace)
		maximum := *survivor
		maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
		e.Mu.Lock()
		next := catalog.CloneState(e.Catalog.State)
		next.SetDescriptor(survivor.Address(), &maximum)
		e.Catalog.PublishLocked(next)
		e.Mu.Unlock()
		closeOwner()
		extensionTemporaryGoneUnit(t, e, ext)
		if e.Catalog.State.Get(peerNamespace.Address()) != peerNamespace || owner.Session.TemporaryNamespace != (ids.ObjectAddress{}) {
			t.Fatal("physical retirement touched peer namespace or retained owner authority")
		}
		current := e.Catalog.State.MustRelation(survivor.Address())
		if current.Revision() != maximum.Revision() || current == &maximum || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 {
			t.Fatal("terminal revision exhaustion bypassed identity replacement")
		}
		values := make([]sqltype.Value, 1)
		ok, err := held.Rows.Next(context.Background(), values)
		text, _ := values[0].Text()
		if err != nil || !ok || text != "Hotel" {
			t.Fatal("retained peer rows", text, ok, err)
		}
		if err := held.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("structural_failure_is_terminal", func(t *testing.T) {
		e, owner, _, ext, _ := extensionTemporaryInstalled(t)
		original := e.Catalog.State
		e.Mu.Lock()
		broken := catalog.CloneState(original)
		broken.RemoveDescriptor(ext.Members[4])
		e.Catalog.PublishLocked(broken)
		e.Mu.Unlock()
		t.Cleanup(func() { e.Mu.Lock(); e.Catalog.PublishLocked(original); e.Mu.Unlock() })
		result := make(chan error, 1)
		go func() { result <- EngineEndTemporarySession(e, owner.Session) }()
		select {
		case err := <-result:
			if err == nil || !strings.Contains(err.Error(), "extension") {
				t.Fatal("structural retirement must return its error", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("structural retirement spun instead of returning")
		}
		if e.Catalog.State != broken || e.Catalog.State.Get(ext.Address()) != ext {
			t.Fatal("failed cleanup partially installed")
		}
	})
}
func TestExtensionTemporaryLookup(t *testing.T) {
	e, owner, installer, ext, _ := extensionTemporaryInstalled(t)
	namespace := e.Catalog.State.MustNamespace(ext.TargetNamespace)
	roleExecute(t, e, owner, "SET search_path TO "+namespace.Name())
	roleExecute(t, e, installer, "SET search_path TO "+namespace.Name())
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: "unaccent"}}}
	e.Mu.Lock()
	ownAuth, err := e.AuthorizationLocked(owner)
	foreignAuth, foreignErr := e.AuthorizationLocked(installer)
	e.Mu.Unlock()
	if err != nil || foreignErr != nil {
		t.Fatal(err, foreignErr)
	}
	ownPath, err := LookupPath(ownAuth, analyze.InstalledFunctionLookup)
	if err != nil || slices.Contains(ownPath, namespace.Address()) {
		t.Fatal("own TEMP helpers must be excluded", ownPath, err)
	}
	path, err := LookupPath(foreignAuth, analyze.InstalledFunctionLookup)
	if err != nil || !slices.Contains(path, namespace.Address()) {
		t.Fatal("authorized foreign installed path omitted", path, err)
	}
	selected, err := EngineLookupNamespaces(e, foreignAuth, qualified, analyze.InstalledFunctionLookup)
	if err != nil || analyze.ProcedureInNamespaces(foreignAuth.Catalog, selected, "unaccent", catalog.UnaccentTextSignature) == nil {
		t.Fatal("real foreign member lookup", selected, err)
	}
	for _, kind := range []analyze.LookupKind{analyze.RelationLookup, analyze.TypeLookup, analyze.FunctionLookup, analyze.OperatorLookup, analyze.CollationLookup, analyze.ConversionLookup} {
		selected, err := EngineLookupNamespaces(e, foreignAuth, qualified, kind)
		if err != nil || selected.Qualified != namespace.Address() {
			t.Fatal("captured superuser namespace USAGE denied", kind, selected, err)
		}
	}
	for _, kind := range []analyze.LookupKind{analyze.FunctionLookup, analyze.OperatorLookup, analyze.CollationLookup, analyze.ConversionLookup} {
		path, err := LookupPath(foreignAuth, kind)
		if err != nil || slices.Contains(path, namespace.Address()) {
			t.Fatal("general TEMP path isolation", kind, path, err)
		}
	}
	roleAttributeCreate(t, e, installer, "ext_temp_actor", true, parser.RoleOptions{})
	actor, _ := extensionTemporaryOperation(t, e, "ext_temp_actor")
	roleExecute(t, e, actor, "SET search_path TO "+namespace.Name())
	e.Mu.Lock()
	denied, err := e.AuthorizationLocked(actor)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []analyze.LookupKind{analyze.InstalledFunctionLookup, analyze.RelationLookup, analyze.TypeLookup, analyze.FunctionLookup, analyze.OperatorLookup, analyze.CollationLookup, analyze.ConversionLookup} {
		_, err := EngineLookupNamespaces(e, denied, qualified, kind)
		extensionUnitError(t, err, "42501")
	}
	path, err = LookupPath(denied, analyze.InstalledFunctionLookup)
	if err != nil || slices.Contains(path, namespace.Address()) {
		t.Fatal("denied TEMP schema was not implicitly omitted", path, err)
	}
}
func TestExtensionTemporaryReplacement(t *testing.T) {
	for _, reason := range []string{"revoke", "inherit", "recreated_role"} {
		t.Run("foreign_private_"+reason, func(t *testing.T) {
			target := NewWithSettings(nil, "ext_temp_fence_db", false)
			owner, _ := extensionTemporaryOperation(t, target, "postgres")
			admin, _ := extensionTemporaryOperation(t, target, "postgres")
			roleExecute(t, target, owner, "CREATE TEMP TABLE fenced_temp_seed(id INT)")
			actor := roleAttributeCreate(t, target, admin, "fenced_temp_installer", true, parser.RoleOptions{})
			grantee := actor.Name
			if reason == "inherit" {
				roleAttributeCreate(t, target, admin, "fenced_temp_group", false, parser.RoleOptions{})
				grantee = "fenced_temp_group"
				roleAttributeApply(t, target, admin, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: grantee}}, Members: []parser.RoleSpec{{Name: actor.Name}}, Options: []parser.MembershipOption{{Name: "inherit", Value: true}}})
			}
			grant := extensionUnitGrant("ext_temp_fence_db", grantee)
			if reason == "recreated_role" {
				grant.Grantees = []parser.GrantGrantee{{Public: true}}
			}
			if err := extensionUnitExecute(t, target, admin, grant); err != nil {
				t.Fatal(err)
			}
			caller, _ := extensionTemporaryOperation(t, target, actor.Name)
			scope, err := target.StartStatement(caller)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !scope.Finished {
					scope.Finish(context.Canceled)
				}
			})
			namespace := target.Catalog.State.Get(owner.Session.TemporaryNamespace).Name()
			_, err = scope.Execute(statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal("foreign staged helpers", err)
			}
			switch reason {
			case "revoke":
				grant.Revoke = true
				if err := extensionUnitExecute(t, target, admin, grant); err != nil {
					t.Fatal(err)
				}
			case "inherit":
				roleAttributeApply(t, target, admin, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: grantee}}, Members: []parser.RoleSpec{{Name: actor.Name}}, Options: []parser.MembershipOption{{Name: "inherit", Value: false}}})
			case "recreated_role":
				roleAttributeApply(t, target, admin, &parser.DropRole{Names: []string{actor.Name}})
				fresh := roleAttributeCreate(t, target, admin, actor.Name, true, parser.RoleOptions{})
				if fresh.Ref == actor.Ref {
					t.Fatal("role identity reused")
				}
			}
			before := target.Catalog.State
			if err := scope.Finish(nil); err == nil {
				t.Fatal("changed actual TEMP installation authority passed Finish")
			}
			if target.Catalog.State != before || target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("TEMP role/ACL fence published partial installation")
			}
		})
	}
	t.Run("named_retained_owner", func(t *testing.T) {
		target := NewWithSettings(nil, "ext_temp_owner_db", false)
		owner, _ := extensionTemporaryOperation(t, target, "postgres")
		admin, _ := extensionTemporaryOperation(t, target, "postgres")
		roleExecute(t, target, owner, "CREATE TEMP TABLE retained_owner_seed(id INT)")
		actor := roleAttributeCreate(t, target, admin, "retained_ext_owner", true, parser.RoleOptions{})
		grant := extensionUnitGrant("ext_temp_owner_db", actor.Name)
		if err := extensionUnitExecute(t, target, admin, grant); err != nil {
			t.Fatal(err)
		}
		caller, _ := extensionTemporaryOperation(t, target, actor.Name)
		namespace := target.Catalog.State.Get(owner.Session.TemporaryNamespace).Name()
		if err := extensionUnitExecute(t, target, caller, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace)); err != nil {
			t.Fatal(err)
		}
		installed := target.Catalog.State.MustExtension(target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		grant.Revoke = true
		if err := extensionUnitExecute(t, target, admin, grant); err != nil {
			t.Fatal(err)
		}
		if _, err := replaceWithFreshCatalog(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		if target.Catalog.State.Get(installed.Address()) != installed || installed.Owner != actor.Ref {
			t.Fatal("replacement substituted named TEMP owner authority")
		}
		before := target.RoleStore.Published
		roleError(t, target, admin, "DROP ROLE "+actor.Name, "2BP01", `role "retained_ext_owner" cannot be dropped because some objects depend on it`, "owner of extension unaccent")
		if target.RoleStore.Published != before {
			t.Fatal("retained owner dependency changed roles")
		}
		if err := EngineEndTemporarySession(target, owner.Session); err != nil {
			t.Fatal(err)
		}
		roleExecute(t, target, admin, "DROP ROLE "+actor.Name)
		recreated := roleAttributeCreate(t, target, admin, actor.Name, true, parser.RoleOptions{})
		if recreated.Ref == actor.Ref {
			t.Fatal("retired owner principal identity reused")
		}
	})
	e, owner, installer, ext, _ := extensionTemporaryInstalled(t)
	namespaceName := e.Catalog.State.Get(ext.TargetNamespace).Name()
	roleExecute(t, e, owner, "CREATE TEMP TABLE ext_retained_local(v TEXT DEFAULT "+namespaceName+".unaccent('Hôtel'::text),CHECK("+namespaceName+".unaccent(v)=v))")
	roleExecute(t, e, owner, "CREATE TEMP VIEW ext_retained_view AS SELECT "+namespaceName+".unaccent('Hôtel'::text) AS v")
	localAddress := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "ext_retained_local"})
	viewAddress := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "ext_retained_view"})
	local := e.Catalog.State.MustRelation(localAddress)
	retainedView := e.Catalog.State.MustRelation(viewAddress)
	var procedureRef ids.ObjectRef
	for ref := range stored.ViewOf(retainedView).References() {
		if ref.Address == ext.Members[1] {
			procedureRef = ref
		}
	}
	if procedureRef.Epoch == 0 {
		t.Fatal("real view failed to capture its procedure manifest")
	}
	source := New()
	sourceOp, closeSource := extensionTemporaryOperation(t, source, "postgres")
	defer closeSource()
	source.Mu.Lock()
	source.Catalog.InstallOIDFrontierLocked(ext.Members[1].ObjectID)
	source.Mu.Unlock()
	roleExecute(t, source, sourceOp, "CREATE SCHEMA ext_imported")
	collision := source.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ext_imported"})
	if collision.ObjectID != ext.Members[1].ObjectID {
		t.Fatal("setup did not create genuine cross-class identity collision")
	}
	original := e.Catalog.State
	if _, err := control.PersistentReplacementCatalog(e.CatalogRead()); err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" {
		t.Fatal("source TEMP transfer must fail", err)
	}
	if e.Catalog.State != original {
		t.Fatal("source projection mutated published authority")
	}
	destination := New()
	destinationOriginal := destination.Catalog.State
	if _, err := destination.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" {
		t.Fatal("actual foreign replacement transferred a TEMP provider", err)
	}
	if destination.Catalog.State != destinationOriginal || e.Catalog.State != original {
		t.Fatal("foreign portability failure changed source or target")
	}
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal("destination TEMP overlay", err)
	}
	for _, address := range append([]ids.ObjectAddress{ext.Address(), ext.TargetNamespace}, ext.Members[:]...) {
		if e.Catalog.State.Get(address) != original.Get(address) {
			t.Fatal("retained provider identity changed", address)
		}
	}
	if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(ext)) != ext.Address() {
		t.Fatal("global TEMP extension name lost")
	}
	for _, member := range ext.Members {
		d := e.Catalog.State.Get(member)
		if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(d)) != member {
			t.Fatal("signature key lost", member)
		}
		if err := e.Catalog.Providers[member.ClassID].Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
	}
	if e.Catalog.State.Get(localAddress) != local || e.Catalog.State.Get(viewAddress) != retainedView {
		t.Fatal("replacement changed retained TEMP consumers")
	}
	if !stored.ViewReferenceValid(e.CatalogRead(), retainedView, procedureRef) {
		t.Fatal("exact retained procedure manifest lost contextual epoch authority")
	}
	if err := table.ValidateDefaultProviders(e.CatalogRead(), local); err != nil {
		t.Fatal("retained procedure DEFAULT authority", err)
	}
	procedureCopy := *e.Catalog.State.MustProcedure(procedureRef.Address)
	substituted := catalog.CloneState(e.Catalog.State)
	substituted.SetDescriptor(procedureRef.Address, &procedureCopy)
	if !stored.ViewReferenceValid(e.Catalog.Read(substituted), retainedView, procedureRef) {
		t.Fatal("compatible COW procedure metadata lost captured view lifetime")
	}
	invalidProcedure := procedureCopy
	invalidProcedure.Strict = !invalidProcedure.Strict
	substituted.SetDescriptor(procedureRef.Address, &invalidProcedure)
	if stored.ViewReferenceValid(e.Catalog.Read(substituted), retainedView, procedureRef) {
		t.Fatal("changed procedure semantics substituted captured view lifetime")
	}
	foreignNamespace := *e.Catalog.State.MustNamespace(ext.TargetNamespace)
	foreignNamespace.Temporary = installer.Session.SessionToken
	foreign := catalog.CloneState(e.Catalog.State)
	foreign.SetDescriptor(ext.TargetNamespace, &foreignNamespace)
	if stored.ViewReferenceValid(e.Catalog.Read(foreign), retainedView, procedureRef) {
		t.Fatal("foreign physical namespace token substituted retained procedure authority")
	}
	if err := e.Catalog.ValidateObjectRefLocked(e.CatalogRead(), procedureRef); err == nil {
		t.Fatal("retained manifest exception widened ordinary stale ObjectRef validity")
	}
	roleExecute(t, e, owner, "INSERT INTO ext_retained_local DEFAULT VALUES")
	result := roleExecute(t, e, owner, "SELECT v FROM ext_retained_view")
	values := make([]sqltype.Value, 1)
	ok, err := result.Rows.Next(context.Background(), values)
	retainedText, _ := values[0].Text()
	if err != nil || !ok || retainedText != "Hotel" {
		t.Fatal("retained TEMP provider view execution", retainedText, ok, err)
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	imported := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ext_imported"})
	if imported.ObjectID == collision.ObjectID || e.Catalog.State.Get(imported) == nil {
		t.Fatal("imported namespace was not reminted around exact TEMP provider identity")
	}
	if err := extensionUnitExecute(t, source, sourceOp, statement(t, "CREATE EXTENSION unaccent")); err != nil {
		t.Fatal(err)
	}
	before := e.Catalog.State
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil || !strings.Contains(err.Error(), "conflicts with live temporary extension") {
		t.Fatal("imported global name conflict", err)
	}
	if e.Catalog.State != before {
		t.Fatal("name conflict changed destination")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(canceled, source, source.CatalogRead()); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replacement", err)
	}
	if e.Catalog.State != before {
		t.Fatal("canceled replacement published")
	}
	binder := NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, installer, nil)
	name := e.Catalog.State.Get(ext.TargetNamespace).Name()
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}, {Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	text, _ := value.Text()
	if err != nil || text != "Hotel" {
		t.Fatal("retained provider execution", text, err)
	}
	if err := EngineEndTemporarySession(e, owner.Session); err != nil {
		t.Fatal(err)
	}
	extensionTemporaryGoneUnit(t, e, ext)
}
func TestExtensionSchemaClosure(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, op, ext)
	base := e.Catalog.State
	restrictStmt := statement(t, "DROP SCHEMA public")
	e.Mu.Lock()
	stage, err := stageSchemaLocked(e, op, restrictStmt, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || !strings.Contains(diagnostic.Detail, "extension unaccent depends on schema public") || stage != nil || e.Catalog.State != base {
		t.Fatal("schema restriction did not use genuine extension dependency", stage, err)
	}
	// Prepare a real CASCADE candidate, then introduce a late dependent. The
	// publication token must reject the entire predecessor rather than prune it.
	e.Mu.Lock()
	candidate, err := stageSchemaLocked(e, op, statement(t, "DROP SCHEMA public CASCADE"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(candidate) })
	roleExecute(t, e, op, "CREATE VIEW public.ext_schema_late AS SELECT v FROM public.ext_chain")
	prepared, err := e.ValidateCatalogStage(context.Background(), candidate)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: candidate.Base.Epoch()})
	}
	if err == nil {
		t.Fatal("late schema dependent bypassed publication fence")
	}
	if e.Catalog.State.Get(survivor.Address()) != survivor || !reflect.DeepEqual(e.Catalog.State.Get(ext.Address()), ext) {
		t.Fatal("failed schema candidate published partially")
	}
}
