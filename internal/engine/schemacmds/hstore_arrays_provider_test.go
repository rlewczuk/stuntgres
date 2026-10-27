package schemacmds_test

import (
	"context"
	"fmt"
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
	if err != nil || !found || len(owners.Members) != extension.HstoreMemberCount-1 || owners.Members[3].Signature != "hstore" || owners.Members[extension.HstoreArrayOperatorSlot].Signature != "0,hstore" {
		t.Fatal("portable roles", owners, err)
	}
	base := ext.Members[0].ObjectID
	for _, tc := range []struct {
		slot                    int
		name, signature, source string
		result                  ids.OID
		strict                  bool
	}{
		{extension.HstoreTextConstructorSlot, "hstore", "25,25", "hstore_from_text", base, false},
		{extension.HstoreArraysConstructorSlot, "hstore", "1009,1009", "hstore_from_arrays", base, false},
		{extension.HstoreArrayConstructorSlot, "hstore", "1009", "hstore_from_array", base, true},
		{extension.HstoreFetchSlot, "fetchval", fmt.Sprintf("%d,25", base), "hstore_fetchval", 25, true},
		{extension.HstoreSliceSlot, "slice_array", fmt.Sprintf("%d,1009", base), "hstore_slice_to_array", 1009, true},
		{extension.HstoreExistsSlot, "exist", fmt.Sprintf("%d,25", base), "hstore_exists", 16, true},
		{extension.HstoreDefinedSlot, "defined", fmt.Sprintf("%d,25", base), "hstore_defined", 16, true},
	} {
		p := view.MustProcedure(ext.Members[tc.slot])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != tc.source || p.Display.Source != tc.source || string(p.Signature) != tc.signature || p.Result != tc.result || p.Strict != tc.strict || p.RoutineKind != catalog.FunctionRoutine {
			t.Fatal("selected declaration", tc.slot, p, err)
		}
		if name, err := extension.ProcedureInstallationName(view, p); err != nil || name != tc.name {
			t.Fatal("selected SQL declaration name", tc.slot, name, err)
		}
		renamed := *p
		renamed.ObjectName = "relabeled_" + tc.name
		renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		state := catalog.CloneState(view.State())
		state.UnbindName(catalog.DescriptorNameKey(p))
		state.SetDescriptor(renamed.Address(), &renamed)
		state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
		current := e.Catalog.Read(state)
		if name, err := extension.ProcedureInstallationName(current, &renamed); err != nil || name != tc.name {
			t.Fatal("relabel changed original SQL name", tc.slot, name, err)
		}
		if symbol, err := extension.ProcedureSymbol(current, &renamed); err != nil || symbol != tc.source {
			t.Fatal("relabel changed executable symbol", tc.slot, symbol, err)
		}
		if _, err := extension.ProcedureInstallationName(current, p); err == nil {
			t.Fatal("stale selected declaration accepted", tc.slot)
		}
	}
	for _, tc := range []struct {
		slot, backer  int
		right, result ids.OID
	}{{extension.HstoreFetchOperatorSlot, extension.HstoreFetchSlot, 25, 25}, {extension.HstoreSliceOperatorSlot, extension.HstoreSliceSlot, 1009, 1009}} {
		op, ok := view.Operator(ext.Members[tc.slot])
		if !ok || op.Left != base || op.Right != tc.right || op.Result != tc.result || op.Function != ext.Members[tc.backer].ObjectID {
			t.Fatal("binary lookup backer graph")
		}
	}
	for slot, signature := range []string{"25,25", "1009,1009", "1009", "hstore,25", "hstore,1009", "hstore,25", "hstore,25", "hstore,25", "hstore,1009"} {
		if owners.Members[slot+extension.HstoreTextConstructorSlot-1].Signature != signature {
			t.Fatal("mixed portable signature", slot+extension.HstoreTextConstructorSlot)
		}
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
		func(_ *catalog.State, d *catalog.Extension) {
			d.Members[extension.HstoreTextConstructorSlot] = d.Members[0]
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreTextConstructorSlot])
			p.Strict = true
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreArraysConstructorSlot])
			p.Signature = "25,25"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreArrayConstructorSlot])
			p.Result = 25
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreFetchSlot])
			p.Implementation = extension.StockSeg
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreSliceSlot])
			p.Display.Source = "hstore_fetchval"
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			p := *s.MustProcedure(d.Members[extension.HstoreExistsSlot])
			p.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Get(d.Members[extension.HstoreFetchOperatorSlot]).(*catalog.Operator)
			p := *o
			p.Function = d.Members[extension.HstoreSliceSlot].ObjectID
			s.SetDescriptor(p.Address(), &p)
		},
		func(s *catalog.State, d *catalog.Extension) {
			o, _ := s.Get(d.Members[extension.HstoreSliceOperatorSlot]).(*catalog.Operator)
			p := *o
			p.Result = 25
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
