package schemacmds_test

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

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func extensionTemporaryOperation(t *testing.T, e *core.Engine, name string) (*session.Operation, func()) {
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
func extensionTemporaryInstalled(t *testing.T) (*core.Engine, *session.Operation, *session.Operation, *catalog.Extension, func()) {
	t.Helper()
	e := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, e, "postgres")
	installer, _ := extensionTemporaryOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, owner, "CREATE TEMP TABLE ext_seed(id INT)")
	namespace := e.Catalog.State.MustNamespace(owner.Session.TemporaryNamespace)
	if err := extensionUnitExecute(t, e, installer, enginetest.Statement(t, "CREATE EXTENSION unaccent SCHEMA "+namespace.Name())); err != nil {
		t.Fatal(err)
	}
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	return e, owner, installer, ext, closeOwner
}
func extensionTemporaryConsumerUnits(t *testing.T, e *core.Engine, op *session.Operation, ext *catalog.Extension) *catalog.Relation {
	t.Helper()
	name := e.Catalog.State.Get(ext.TargetNamespace).Name()
	for _, q := range []string{
		fmt.Sprintf("CREATE TABLE public.ext_survivor(id INT,txt TEXT DEFAULT %s.unaccent('Hôtel'::text),CONSTRAINT ext_check CHECK(%s.unaccent(txt)=txt))", name, name),
		"CREATE INDEX ext_survivor_id ON public.ext_survivor(id)",
		"INSERT INTO public.ext_survivor(id) VALUES(1)",
		fmt.Sprintf("CREATE VIEW public.ext_view AS SELECT %s.unaccent('Hôtel'::text) AS v", name),
		"CREATE VIEW public.ext_chain AS SELECT v FROM public.ext_view",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	return e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ext_survivor"}))
}
func extensionTemporaryGoneUnit(t *testing.T, e *core.Engine, ext *catalog.Extension) {
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
	edges, err := pgcatalog.CollectDepend(context.Background(), e.CatalogRead(), core.EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges {
		if edge.Referenced == ext.Address() || edge.Dependent == ext.Address() || slices.Contains(ext.Members[:], edge.Referenced) || slices.Contains(ext.Members[:], edge.Dependent) {
			t.Fatal("dangling retirement edge", edge)
		}
	}
}
func TestExtensionSchemaClosure(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	survivor := extensionTemporaryConsumerUnits(t, e, op, ext)
	base := e.Catalog.State
	restrictStmt := enginetest.Statement(t, "DROP SCHEMA public")
	e.Mu.Lock()
	stage, err := schemacmds.StageSchemaLocked(e, op, restrictStmt, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || !strings.Contains(diagnostic.Detail, "extension unaccent depends on schema public") || stage != nil || e.Catalog.State != base {
		t.Fatal("schema restriction did not use genuine extension dependency", stage, err)
	}
	// Prepare a real CASCADE candidate, then introduce a late dependent. The
	// publication token must reject the entire predecessor rather than prune it.
	e.Mu.Lock()
	candidate, err := schemacmds.StageSchemaLocked(e, op, enginetest.Statement(t, "DROP SCHEMA public CASCADE"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(candidate) })
	enginetest.RoleExecute(t, e, op, "CREATE VIEW public.ext_schema_late AS SELECT v FROM public.ext_chain")
	prepared, err := e.ValidateCatalogStage(context.Background(), candidate)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: candidate.Base.Epoch()})
	}
	if err == nil {
		t.Fatal("late schema dependent bypassed publication fence")
	}
	if e.Catalog.State.Get(survivor.Address()) != survivor || !reflect.DeepEqual(e.Catalog.State.Get(ext.Address()), ext) {
		t.Fatal("failed schema candidate published partially")
	}
}
