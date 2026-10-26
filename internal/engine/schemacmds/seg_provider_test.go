package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func segProviderStage(t *testing.T, e *core.Engine, op *session.Operation) *core.CatalogStage {
	t.Helper()
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "seg"}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}
func segProviderInstalled(t *testing.T) (*core.Engine, *session.Operation, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	stage := segProviderStage(t, e, op)
	publication, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalogOperation(op, nil, publication, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	v := e.CatalogRead()
	ext := v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}))
	return e, op, ext, v.MustType(ext.Members[0])
}
func TestSegProviderIdentity(t *testing.T) {
	e, _, ext, base := segProviderInstalled(t)
	view := e.CatalogRead()
	if ext.Version != "1.4" || len(ext.Members) != 3 || !base.SQLType.IsSeg() {
		t.Fatal("manifest")
	}
	if err := (stored.TypeProvider{}).Validate(view, base); err != nil {
		t.Fatal(err)
	}
	array, ok := view.LookupArray(base.SQLType)
	if !ok || array.Concrete() || array.Element() != base.SQLType || array.ID() != base.Array {
		t.Fatal("companion")
	}
	for i, name := range []string{"seg_in", "seg_out"} {
		p := view.MustProcedure(ext.Members[i+1])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != name || extension.SegProviderOf(p) != extension.StockSeg {
			t.Fatal("codec authority", symbol, err)
		}
	}
	facts, err := pgcatalog.TypeFacts(t.Context(), view, base, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if facts.Input != uint32(ext.Members[1].ObjectID) || facts.Output != uint32(ext.Members[2].ObjectID) {
		t.Fatal("catalog codec identities do not name installed providers", facts)
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	owners, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || owners.Version != "1.4" || len(owners.Members) != 3 || owners.Members[2].Signature != "seg" {
		t.Fatal("portable roles", owners, err)
	}
	for _, corrupt := range []func(*catalog.State, *catalog.Extension){
		func(_ *catalog.State, d *catalog.Extension) { d.Members = d.Members[:2] },
		func(_ *catalog.State, d *catalog.Extension) { d.Members[2] = d.Members[1] },
		func(_ *catalog.State, d *catalog.Extension) { d.Version = "1.3" },
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[1])
			p.Result = 25
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[2])
			p.Signature = "25"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[2])
			p.Display.Source = "seg_in"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[1])
			p.Implementation = extension.StockIntarray
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			b := *s.MustType(d.Members[0])
			b.Array = 0
			s.SetDescriptor(b.Address(), &b)
		},
		func(s *catalog.State, d *catalog.Extension) {
			b := *s.MustType(d.Members[0])
			b.SQLType, _ = sqltype.QueryInt(b.SQLType.ID(), b.Name(), b.SQLType.SQLName())
			s.SetDescriptor(b.Address(), &b)
		},
	} {
		candidate := catalog.CloneState(view.State())
		bad := *ext
		bad.Members = slices.Clone(ext.Members)
		candidate.SetDescriptor(bad.Address(), &bad)
		corrupt(candidate, &bad)
		if err := extension.StockSeg.ValidateExtension(e.Catalog.Read(candidate), &bad); err == nil {
			t.Fatal("corrupt provider accepted")
		}
		if view.MustType(base.Address()) != base {
			t.Fatal("validation mutated graph")
		}
	}
}
func TestSegProviderPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead().State()
	stage := segProviderStage(t, e, op)
	view := e.StageRead(stage)
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}))
	view.MustProcedure(ext.Members[2]).Result = 25
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("invalid graph accepted")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("partial publication")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); err == nil {
		t.Fatal("canceled graph accepted")
	}
}

func TestSegProviderPublicationConflict(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION intarray")
	enginetest.Run(t, e, "ALTER FUNCTION bqarr_in(cstring) RENAME TO seg_in")
	before := e.CatalogRead()
	collision := before.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "seg_in", Signature: "2275"})
	original := before.Get(collision)
	if original == nil {
		t.Fatal("missing genuine collision")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	_, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "seg"}})
	e.Mu.Unlock()
	if err == nil || e.CatalogRead().State() != before.State() || e.CatalogRead().Get(collision) != original {
		t.Fatal("collision published fragments", err)
	}
	if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}) != (ids.ObjectAddress{}) {
		t.Fatal("failed provider installed")
	}
}

func TestSegProviderRemap(t *testing.T) {
	source, _, ext, base := segProviderInstalled(t)
	target := engine.New()
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	op, done := enginetest.RoleOperation(t, target, "postgres")
	defer done()
	stage := segProviderStage(t, target, op)
	publication, err := target.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.PublishCatalogOperation(op, nil, publication, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	destination := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}))
	destBase := view.MustType(destination.Members[0])
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): destination.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(destBase.Array)}}
	for i, a := range ext.Members {
		mapping[a] = destination.Members[i]
	}
	for _, a := range ext.Members[1:] {
		old := source.CatalogRead().MustProcedure(a)
		next := extension.RemapDescriptor(old, mapping).(*catalog.Procedure)
		if next.Implementation != extension.StockSeg || next.Extension != destination.Address() {
			t.Fatal("procedure remap authority")
		}
		expected := view.MustProcedure(mapping[a])
		if next.Signature != expected.Signature || next.Result != expected.Result {
			t.Fatal("procedure signature remap")
		}
	}
}
