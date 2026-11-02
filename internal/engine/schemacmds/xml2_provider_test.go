package schemacmds_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func xml2UnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "xml2"}})
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	v := e.CatalogRead()
	return e, v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
}

func TestXML2ProviderIdentity(t *testing.T) {
	e, ext := xml2UnitInstalled(t)
	v := e.CatalogRead()
	control := extension.StockXML2.Control()
	if control.Version != "1.2" || control.Trusted || !control.Superuser || control.Relocatable || control.Comment == nil || *control.Comment != "XPath querying and XSLT" || len(ext.Members) != 4 {
		t.Fatal("release control", control, ext)
	}
	versions := extension.StockXML2.Versions()
	if !slices.Equal(versions.Bases, []string{"1.2"}) || len(versions.Edges) != 0 || extension.ValidateActions(extension.StockXML2) != nil {
		t.Fatal("current-only graph", versions)
	}
	versions.Bases[0] = "changed"
	if extension.StockXML2.Versions().Bases[0] != "1.2" {
		t.Fatal("borrowed version backing")
	}
	names := []string{"xml_valid", "xpath_string", "xpath_number", "xpath_bool"}
	results := []ids.OID{16, 25, 700, 16}
	for slot, address := range ext.Members {
		p := v.MustProcedure(address)
		signature, volatility := "25,25", expr.Immutable
		display := catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: names[slot], Binary: "$libdir/pgxml", HasBinary: true}
		if slot == 0 {
			signature = "25"
			volatility = expr.Stable
			display = catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "xml_is_well_formed"}
		}
		if p.Name() != names[slot] || p.Signature != catalog.ProcedureSignature(signature) || p.Result != results[slot] || p.Volatility != volatility || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 's'}) || p.Display != display || p.Owner != ext.Owner || p.Namespace() != ext.TargetNamespace || p.Origin.Declaration != slot || p.Origin.Installation != ext.Installation {
			t.Fatal("released declaration", slot, p)
		}
		if symbol, err := extension.ProcedureSymbol(v, p); err != nil || symbol != names[slot] {
			t.Fatal("sealed selector", symbol, err)
		}
		if err := extension.StockXML2.ValidateProcedure(v, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := extension.StockXML2.ValidateExtension(v, ext); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xpath_nodeset", "xpath_list", "xpath_table", "xslt_process", "xml_encode_special_chars"} {
		for p := range catalog.DefinitionProceduresNamedAt(v, nil, ext.TargetNamespace, name) {
			t.Fatal("adjacent function", p)
		}
	}
	original := v.MustProcedure(ext.Members[3])
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = &extension.XML2Provider{} },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { copy := *p.Origin.Installation; p.Origin.Installation = &copy },
		func(p *catalog.Procedure) { p.Origin.Declaration = 0 },
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Volatility = expr.Stable },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Installed.Parallel = 'r' },
		func(p *catalog.Procedure) { p.Display.Source = "xpath_string" },
		func(p *catalog.Procedure) { p.Display.Binary = "$libdir/sslinfo" },
	} {
		bad := *original
		origin := *bad.Origin
		bad.Origin = &origin
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockXML2.ValidateProcedure(v.WithState(state), &bad); err == nil {
			t.Fatal("forged provider accepted", bad)
		}
	}
	// Real Stage rollback: a late collision occurs after three private creates.
	for _, slot := range []int{0, 3} {
		base := catalog.CloneState(v.State())
		for i, a := range ext.Members {
			if i != slot {
				p := v.MustProcedure(a)
				base.UnbindName(catalog.DescriptorNameKey(p))
				base.RemoveDescriptor(a)
			}
		}
		before := e.Catalog.State
		stage := e.BeginCatalogStage(v.WithState(base))
		copy := *ext
		e.Mu.Lock()
		err := extension.StockXML2.Stage(&schemacmds.ExtensionHost{Engine: e, Stage: stage}, &copy, ext.Owner)
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || diagnostic.Message != "function \""+names[slot]+"\" already exists with same argument types" || !strings.HasSuffix(diagnostic.Context, map[int]string{0: "extension script file \"xml2--1.1.sql\", near line 9", 3: "extension script file \"xml2--1.1.sql\", near line 29"}[slot]) {
			t.Fatal("released collision", slot, err)
		}
		if e.Catalog.State != before || before.Get(original.Address()) != original || before.Get(ext.Address()) != ext {
			t.Fatal("partial publication")
		}
	}
}
