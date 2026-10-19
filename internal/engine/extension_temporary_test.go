package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
	"time"
)

func extensionTemporaryOperation(t *testing.T, e *Engine, name string) (*Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: ref})
	if err != nil {
		t.Fatal(err)
	}
	var op *Operation
	var once sync.Once
	close := func() {
		once.Do(func() {
			if op != nil {
				op.Done()
			}
			s.RequestEnd(CleanClose)
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
func extensionTemporaryInstalled(t *testing.T) (*Engine, *Operation, *Operation, *catalog.Extension, func()) {
	t.Helper()
	e := New()
	owner, closeOwner := extensionTemporaryOperation(t, e, "postgres")
	installer, _ := extensionTemporaryOperation(t, e, "postgres")
	roleExecute(t, e, owner, "CREATE TEMP TABLE ext_seed(id INT)")
	namespace := e.catalog.state.MustNamespace(owner.session.temporaryNamespace)
	if err := extensionUnitExecute(t, e, installer, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace.Name())); err != nil {
		t.Fatal(err)
	}
	ext := e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	return e, owner, installer, ext, closeOwner
}
func extensionTemporaryConsumerUnits(t *testing.T, e *Engine, op *Operation, ext *catalog.Extension) *catalog.Relation {
	t.Helper()
	name := e.catalog.state.Get(ext.TargetNamespace).Name()
	for _, q := range []string{
		fmt.Sprintf("CREATE TABLE public.ext_survivor(id INT,txt TEXT DEFAULT %s.unaccent('Hôtel'::text),CONSTRAINT ext_check CHECK(%s.unaccent(txt)=txt))", name, name),
		"CREATE INDEX ext_survivor_id ON public.ext_survivor(id)",
		"INSERT INTO public.ext_survivor(id) VALUES(1)",
		fmt.Sprintf("CREATE VIEW public.ext_view AS SELECT %s.unaccent('Hôtel'::text) AS v", name),
		"CREATE VIEW public.ext_chain AS SELECT v FROM public.ext_view",
	} {
		roleExecute(t, e, op, q)
	}
	return e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ext_survivor"}))
}
func extensionTemporaryStage(t *testing.T, e *Engine, ext *catalog.Extension) (*CatalogStage, *temporarySessionIndexes) {
	t.Helper()
	base := e.CatalogRead()
	targets, err := namespaceChildren(base, map[ids.ObjectAddress]bool{ext.TargetNamespace: true})
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(base)
	stage.terminalCleanup = true
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	for _, target := range targets {
		if err := stage.Remove(e.catalog.Domain.Ref(base.Epoch(), target)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	if err := stageOwnedExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	indexes, err := e.prepareTemporarySessionIndexes(stage)
	if err != nil {
		t.Fatal(err)
	}
	return stage, indexes
}
func extensionTemporaryGoneUnit(t *testing.T, e *Engine, ext *catalog.Extension) {
	t.Helper()
	namespace, ok := e.catalog.state.Namespace(ext.TargetNamespace)
	if !ok {
		t.Fatal("physical end removed retained TEMP namespace metadata")
	}
	assertTemporaryNamespacePair(t, e, namespace.Temporary, namespace.Address())
	for _, address := range append([]ids.ObjectAddress{ext.Address()}, ext.Members[:]...) {
		if e.catalog.state.Get(address) != nil {
			t.Fatal("retired provider identity retained", address)
		}
	}
	for key, address := range e.catalog.state.Names() {
		if address == ext.Address() || slices.Contains(ext.Members[:], address) || key.Namespace == ext.TargetNamespace {
			t.Fatal("retired signature/name retained", key)
		}
	}
	edges, err := collectPGDepend(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges {
		if edge.referenced == ext.Address() || edge.dependent == ext.Address() || slices.Contains(ext.Members[:], edge.referenced) || slices.Contains(ext.Members[:], edge.dependent) {
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
		namespace := e.catalog.state.Get(owner.session.temporaryNamespace).Name()
		before := e.catalog.state
		err := extensionUnitExecute(t, e, owner, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace))
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function unaccent_init(internal) does not exist", Context: unaccentTemplateContext}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.catalog.state != before {
			t.Fatal("own staged helper failure did not preserve full authority", err)
		}
		if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}); exists {
			t.Fatal("failed helper install leaked extension")
		}
	})
	survivor := extensionTemporaryConsumerUnits(t, e, installer, ext)
	store := e.storage.Relations[survivor.Address()]
	oldRows, oldEnds := store.Versions, store.Ends
	index := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "ext_survivor_id"})
	oldIndex := e.storage.IndexRoots[index]
	base := e.catalog.state
	stage, indexes := extensionTemporaryStage(t, e, ext)
	replacement := stage.Descriptors[survivor.Address()].(*catalog.Relation)
	if len(replacement.ColumnMeta.Defaults) != 0 || len(replacement.Constraints.Check) != 0 || len(survivor.ColumnMeta.Defaults) != 1 || len(survivor.Constraints.Check) != 1 {
		t.Fatal("candidate changed predecessor or failed expression closure")
	}
	roleExecute(t, e, installer, "CREATE VIEW public.ext_late AS SELECT v FROM public.ext_chain")
	e.mu.Lock()
	installed, err := e.installTemporarySessionStageLocked(owner.session, stage, []ids.ObjectAddress{ext.TargetNamespace}, indexes)
	e.mu.Unlock()
	if err != nil || installed {
		t.Fatal("late dependent bypassed whole-root fence", installed, err)
	}
	if e.catalog.state.Get(survivor.Address()) != survivor || e.catalog.state.Get(ext.Address()) != ext {
		t.Fatal("failed candidate published partially")
	}
	if err := e.endTemporarySession(owner.session); err != nil {
		t.Fatal(err)
	}
	extensionTemporaryGoneUnit(t, e, ext)
	current := e.catalog.state.MustRelation(survivor.Address())
	if current == survivor || len(current.ColumnMeta.Defaults) != 0 || len(current.Constraints.Check) != 0 || e.storage.Relations[survivor.Address()] != store || e.storage.IndexRoots[index] != oldIndex || !sameVersionRoot(oldRows, store.Versions) || oldEnds != store.Ends || e.catalog.state.Epoch() != base.Epoch() {
		t.Fatal("surviving row/index/provenance changed")
	}
	for _, name := range []string{"ext_view", "ext_chain", "ext_late"} {
		if _, exists := e.catalog.state.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: name}); exists {
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
		for _, d := range e.catalog.state.Descriptors() {
			if d.Namespace() == ext.TargetNamespace {
				t.Fatal("mixed provider retirement left a child", d.Address())
			}
		}
	})
	t.Run("private_authority", func(t *testing.T) {
		e, _, _, ext, _ := extensionTemporaryInstalled(t)
		private, err := e.startSession(context.Background(), SessionStart{Authenticated: e.roles.domain.Principal(10)}, true)
		if err != nil {
			t.Fatal(err)
		}
		var op *Operation
		var once sync.Once
		closePrivate := func() {
			once.Do(func() {
				if op != nil {
					op.Done()
				}
				private.RequestEnd(CleanClose)
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
		before := e.catalog.state
		_, err = e.Execute(op, statement(t, "CREATE TEMP TABLE private_extension_seed(id INT)"), 0, nil)
		if err == nil || e.catalog.state != before {
			t.Fatal("private maintenance acquired public TEMP authority", err)
		}
		if err := e.endTemporarySession(private); err != nil || e.catalog.state != before || e.catalog.state.Get(ext.Address()) != ext {
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
		peerNamespace := e.catalog.state.Get(peer.session.temporaryNamespace)
		maximum := *survivor
		maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
		e.mu.Lock()
		next := catalog.CloneState(e.catalog.state)
		next.SetDescriptor(survivor.Address(), &maximum)
		e.catalog.state = next
		e.mu.Unlock()
		closeOwner()
		extensionTemporaryGoneUnit(t, e, ext)
		if e.catalog.state.Get(peerNamespace.Address()) != peerNamespace || owner.session.temporaryNamespace != (ids.ObjectAddress{}) {
			t.Fatal("physical retirement touched peer namespace or retained owner authority")
		}
		current := e.catalog.state.MustRelation(survivor.Address())
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
		original := e.catalog.state
		e.mu.Lock()
		broken := catalog.CloneState(original)
		broken.RemoveDescriptor(ext.Members[4])
		e.catalog.state = broken
		e.mu.Unlock()
		t.Cleanup(func() { e.mu.Lock(); e.catalog.state = original; e.mu.Unlock() })
		result := make(chan error, 1)
		go func() { result <- e.endTemporarySession(owner.session) }()
		select {
		case err := <-result:
			if err == nil || !strings.Contains(err.Error(), "extension") {
				t.Fatal("structural retirement must return its error", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("structural retirement spun instead of returning")
		}
		if e.catalog.state != broken || e.catalog.state.Get(ext.Address()) != ext {
			t.Fatal("failed cleanup partially installed")
		}
	})
}
func TestExtensionTemporaryLookup(t *testing.T) {
	e, owner, installer, ext, _ := extensionTemporaryInstalled(t)
	namespace := e.catalog.state.MustNamespace(ext.TargetNamespace)
	roleExecute(t, e, owner, "SET search_path TO "+namespace.Name())
	roleExecute(t, e, installer, "SET search_path TO "+namespace.Name())
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: namespace.Name()}, {Text: "unaccent"}}}
	e.mu.Lock()
	ownAuth, err := e.authorizationLocked(owner)
	foreignAuth, foreignErr := e.authorizationLocked(installer)
	e.mu.Unlock()
	if err != nil || foreignErr != nil {
		t.Fatal(err, foreignErr)
	}
	ownPath, err := e.lookupPath(ownAuth, installedFunctionLookup)
	if err != nil || slices.Contains(ownPath, namespace.Address()) {
		t.Fatal("own TEMP helpers must be excluded", ownPath, err)
	}
	path, err := e.lookupPath(foreignAuth, installedFunctionLookup)
	if err != nil || !slices.Contains(path, namespace.Address()) {
		t.Fatal("authorized foreign installed path omitted", path, err)
	}
	selected, err := e.lookupNamespaces(foreignAuth, qualified, installedFunctionLookup)
	if err != nil || procedureInNamespaces(foreignAuth.catalog, selected, "unaccent", catalog.UnaccentTextSignature) == nil {
		t.Fatal("real foreign member lookup", selected, err)
	}
	for _, kind := range []lookupKind{relationLookup, typeLookup, functionLookup, operatorLookup, collationLookup, conversionLookup} {
		selected, err := e.lookupNamespaces(foreignAuth, qualified, kind)
		if err != nil || selected.qualified != namespace.Address() {
			t.Fatal("captured superuser namespace USAGE denied", kind, selected, err)
		}
	}
	for _, kind := range []lookupKind{functionLookup, operatorLookup, collationLookup, conversionLookup} {
		path, err := e.lookupPath(foreignAuth, kind)
		if err != nil || slices.Contains(path, namespace.Address()) {
			t.Fatal("general TEMP path isolation", kind, path, err)
		}
	}
	roleAttributeCreate(t, e, installer, "ext_temp_actor", true, parser.RoleOptions{})
	actor, _ := extensionTemporaryOperation(t, e, "ext_temp_actor")
	roleExecute(t, e, actor, "SET search_path TO "+namespace.Name())
	e.mu.Lock()
	denied, err := e.authorizationLocked(actor)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []lookupKind{installedFunctionLookup, relationLookup, typeLookup, functionLookup, operatorLookup, collationLookup, conversionLookup} {
		_, err := e.lookupNamespaces(denied, qualified, kind)
		extensionUnitError(t, err, "42501")
	}
	path, err = e.lookupPath(denied, installedFunctionLookup)
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
				if !scope.finished {
					scope.Finish(context.Canceled)
				}
			})
			namespace := target.catalog.state.Get(owner.session.temporaryNamespace).Name()
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
			before := target.catalog.state
			if err := scope.Finish(nil); err == nil {
				t.Fatal("changed actual TEMP installation authority passed Finish")
			}
			if target.catalog.state != before || target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
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
		namespace := target.catalog.state.Get(owner.session.temporaryNamespace).Name()
		if err := extensionUnitExecute(t, target, caller, statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace)); err != nil {
			t.Fatal(err)
		}
		installed := target.catalog.state.MustExtension(target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		grant.Revoke = true
		if err := extensionUnitExecute(t, target, admin, grant); err != nil {
			t.Fatal(err)
		}
		if _, err := replaceWithFreshCatalog(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		if target.catalog.state.Get(installed.Address()) != installed || installed.Owner != actor.Ref {
			t.Fatal("replacement substituted named TEMP owner authority")
		}
		before := target.roles.published
		roleError(t, target, admin, "DROP ROLE "+actor.Name, "2BP01", `role "retained_ext_owner" cannot be dropped because some objects depend on it`, "owner of extension unaccent")
		if target.roles.published != before {
			t.Fatal("retained owner dependency changed roles")
		}
		if err := target.endTemporarySession(owner.session); err != nil {
			t.Fatal(err)
		}
		roleExecute(t, target, admin, "DROP ROLE "+actor.Name)
		recreated := roleAttributeCreate(t, target, admin, actor.Name, true, parser.RoleOptions{})
		if recreated.Ref == actor.Ref {
			t.Fatal("retired owner principal identity reused")
		}
	})
	e, owner, installer, ext, _ := extensionTemporaryInstalled(t)
	namespaceName := e.catalog.state.Get(ext.TargetNamespace).Name()
	roleExecute(t, e, owner, "CREATE TEMP TABLE ext_retained_local(v TEXT DEFAULT "+namespaceName+".unaccent('Hôtel'::text),CHECK("+namespaceName+".unaccent(v)=v))")
	roleExecute(t, e, owner, "CREATE TEMP VIEW ext_retained_view AS SELECT "+namespaceName+".unaccent('Hôtel'::text) AS v")
	localAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "ext_retained_local"})
	viewAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "ext_retained_view"})
	local := e.catalog.state.MustRelation(localAddress)
	retainedView := e.catalog.state.MustRelation(viewAddress)
	var procedureRef ids.ObjectRef
	for ref := range viewOf(retainedView).references {
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
	source.mu.Lock()
	source.catalog.nextOID = ext.Members[1].ObjectID
	source.mu.Unlock()
	roleExecute(t, source, sourceOp, "CREATE SCHEMA ext_imported")
	collision := source.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ext_imported"})
	if collision.ObjectID != ext.Members[1].ObjectID {
		t.Fatal("setup did not create genuine cross-class identity collision")
	}
	original := e.catalog.state
	if _, err := persistentReplacementCatalog(e.CatalogRead()); err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" {
		t.Fatal("source TEMP transfer must fail", err)
	}
	if e.catalog.state != original {
		t.Fatal("source projection mutated published authority")
	}
	destination := New()
	destinationOriginal := destination.catalog.state
	if _, err := destination.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" {
		t.Fatal("actual foreign replacement transferred a TEMP provider", err)
	}
	if destination.catalog.state != destinationOriginal || e.catalog.state != original {
		t.Fatal("foreign portability failure changed source or target")
	}
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal("destination TEMP overlay", err)
	}
	for _, address := range append([]ids.ObjectAddress{ext.Address(), ext.TargetNamespace}, ext.Members[:]...) {
		if e.catalog.state.Get(address) != original.Get(address) {
			t.Fatal("retained provider identity changed", address)
		}
	}
	if e.catalog.state.NameAddress(catalog.DescriptorNameKey(ext)) != ext.Address() {
		t.Fatal("global TEMP extension name lost")
	}
	for _, member := range ext.Members {
		d := e.catalog.state.Get(member)
		if e.catalog.state.NameAddress(catalog.DescriptorNameKey(d)) != member {
			t.Fatal("signature key lost", member)
		}
		if err := e.catalog.Providers[member.ClassID].Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
	}
	if e.catalog.state.Get(localAddress) != local || e.catalog.state.Get(viewAddress) != retainedView {
		t.Fatal("replacement changed retained TEMP consumers")
	}
	if !storedViewReferenceValid(e.CatalogRead(), retainedView, procedureRef) {
		t.Fatal("exact retained procedure manifest lost contextual epoch authority")
	}
	if err := validateDefaultProviders(e.CatalogRead(), local); err != nil {
		t.Fatal("retained procedure DEFAULT authority", err)
	}
	procedureCopy := *e.catalog.state.MustProcedure(procedureRef.Address)
	substituted := catalog.CloneState(e.catalog.state)
	substituted.SetDescriptor(procedureRef.Address, &procedureCopy)
	if !storedViewReferenceValid(e.catalog.read(substituted), retainedView, procedureRef) {
		t.Fatal("compatible COW procedure metadata lost captured view lifetime")
	}
	invalidProcedure := procedureCopy
	invalidProcedure.Strict = !invalidProcedure.Strict
	substituted.SetDescriptor(procedureRef.Address, &invalidProcedure)
	if storedViewReferenceValid(e.catalog.read(substituted), retainedView, procedureRef) {
		t.Fatal("changed procedure semantics substituted captured view lifetime")
	}
	foreignNamespace := *e.catalog.state.MustNamespace(ext.TargetNamespace)
	foreignNamespace.Temporary = installer.session.token
	foreign := catalog.CloneState(e.catalog.state)
	foreign.SetDescriptor(ext.TargetNamespace, &foreignNamespace)
	if storedViewReferenceValid(e.catalog.read(foreign), retainedView, procedureRef) {
		t.Fatal("foreign physical namespace token substituted retained procedure authority")
	}
	if err := e.catalog.validateObjectRefLocked(e.CatalogRead(), procedureRef); err == nil {
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
	imported := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ext_imported"})
	if imported.ObjectID == collision.ObjectID || e.catalog.state.Get(imported) == nil {
		t.Fatal("imported namespace was not reminted around exact TEMP provider identity")
	}
	if err := extensionUnitExecute(t, source, sourceOp, statement(t, "CREATE EXTENSION unaccent")); err != nil {
		t.Fatal(err)
	}
	before := e.catalog.state
	if _, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil || !strings.Contains(err.Error(), "conflicts with live temporary extension") {
		t.Fatal("imported global name conflict", err)
	}
	if e.catalog.state != before {
		t.Fatal("name conflict changed destination")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(canceled, source, source.CatalogRead()); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replacement", err)
	}
	if e.catalog.state != before {
		t.Fatal("canceled replacement published")
	}
	binder := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, installer, nil)
	name := e.catalog.state.Get(ext.TargetNamespace).Name()
	node, err := binder.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}, {Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	text, _ := value.Text()
	if err != nil || text != "Hotel" {
		t.Fatal("retained provider execution", text, err)
	}
	if err := e.endTemporarySession(owner.session); err != nil {
		t.Fatal(err)
	}
	extensionTemporaryGoneUnit(t, e, ext)
}
func TestExtensionSchemaClosure(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, op, ext)
	base := e.catalog.state
	restrictStmt := statement(t, "DROP SCHEMA public")
	e.mu.Lock()
	stage, err := e.stageSchemaLocked(op, restrictStmt, sqltype.InputEnvironment{})
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || !strings.Contains(diagnostic.Detail, "extension unaccent depends on schema public") || stage != nil || e.catalog.state != base {
		t.Fatal("schema restriction did not use genuine extension dependency", stage, err)
	}
	// Prepare a real CASCADE candidate, then introduce a late dependent. The
	// publication token must reject the entire predecessor rather than prune it.
	e.mu.Lock()
	candidate, err := e.stageSchemaLocked(op, statement(t, "DROP SCHEMA public CASCADE"), sqltype.InputEnvironment{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(candidate) })
	roleExecute(t, e, op, "CREATE VIEW public.ext_schema_late AS SELECT v FROM public.ext_chain")
	prepared, err := e.ValidateCatalogStage(context.Background(), candidate)
	if err == nil {
		_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: candidate.Base.Epoch()})
	}
	if err == nil {
		t.Fatal("late schema dependent bypassed publication fence")
	}
	if e.catalog.state.Get(survivor.Address()) != survivor || !reflect.DeepEqual(e.catalog.state.Get(ext.Address()), ext) {
		t.Fatal("failed schema candidate published partially")
	}
}
