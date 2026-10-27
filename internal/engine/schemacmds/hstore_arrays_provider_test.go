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
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func hstoreArraysStage(t *testing.T, e *core.Engine, op *session.Operation) *core.CatalogStage {
	t.Helper()
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "hstore"}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return stage
}
func TestHstoreArraysProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage := hstoreArraysStage(t, e, op)
	publication, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalogOperation(op, nil, publication, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	if len(ext.Members) != extension.HstoreMemberCount || ext.Version != "1.8" {
		t.Fatal("manifest")
	}
	for i, name := range []string{"hstore_in", "hstore_out", "hstore_to_array", "hstore_to_matrix"} {
		proc := view.MustProcedure(ext.Members[i+1])
		symbol, err := extension.ProcedureSymbol(view, proc)
		if err != nil || symbol != name {
			t.Fatal("authentic symbol", symbol, err)
		}
		if i >= 2 && (proc.Result != 1009 || proc.Signature != extension.HstoreSignature(view.MustType(ext.Members[0]).SQLType.ID())) {
			t.Fatal("conversion signature")
		}
	}
	for i := range 2 {
		op, ok := view.Operator(ext.Members[extension.HstoreArrayOperatorSlot+i])
		if !ok || op.Left != 0 || op.Right != ext.Members[0].ObjectID || op.Result != 1009 || op.Function != ext.Members[3+i].ObjectID {
			t.Fatal("prefix identity")
		}
	}
	metadata, err := extension.OwnerMetadata(view, e.RoleStore.PublishedRead(&e.Catalog.Owner, view), ext)
	if err != nil {
		t.Fatal(err)
	}
	owners, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || len(owners.Members) != extension.HstoreMemberCount || owners.Members[3].Signature != "hstore" || owners.Members[extension.HstoreArrayOperatorSlot].Signature != "0,hstore" {
		t.Fatal("portable roles", owners, err)
	}
	for _, corrupt := range []func(*catalog.State, *catalog.Extension){
		func(_ *catalog.State, d *catalog.Extension) { d.Members = d.Members[:3] },
		func(_ *catalog.State, d *catalog.Extension) { d.Members[4] = d.Members[3] },
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[3])
			p.Result = 25
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[4])
			p.Signature = "25"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[3])
			p.Display.Source = "hstore_to_matrix"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[3])
			p.Implementation = extension.StockSeg
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Get(d.Members[extension.HstoreArrayOperatorSlot]).(*catalog.Operator)
			p := *o
			p.Function = d.Members[4].ObjectID
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Get(d.Members[extension.HstoreMatrixOperatorSlot]).(*catalog.Operator)
			p := *o
			p.Left = p.Right
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Get(d.Members[extension.HstoreArrayOperatorSlot]).(*catalog.Operator)
			p := *o
			p.Hash = true
			s.SetDescriptor(p.Address(), &p)
		},
	} {
		candidate := catalog.CloneState(view.State())
		bad := *ext
		bad.Members = slices.Clone(ext.Members)
		candidate.SetDescriptor(bad.Address(), &bad)
		corrupt(candidate, &bad)
		if err := extension.StockHstore.ValidateExtension(e.Catalog.Read(candidate), &bad); err == nil {
			t.Fatal("corrupt manifest admitted")
		}
		if view.MustExtension(ext.Address()) != ext {
			t.Fatal("old graph mutated")
		}
	}
}
func TestHstoreArraysProviderPublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead().State()
	stage := hstoreArraysStage(t, e, op)
	view := e.StageRead(stage)
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "hstore"}))
	view.MustProcedure(ext.Members[3]).Result = 25
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("invalid publication admitted")
	}
	if e.CatalogRead().State() != before {
		t.Fatal("partial publication")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.ValidateCatalogStage(ctx, stage); err == nil {
		t.Fatal("canceled publication admitted")
	}
}
