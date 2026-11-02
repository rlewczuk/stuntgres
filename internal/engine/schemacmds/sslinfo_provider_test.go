package schemacmds_test

import (
	"slices"
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
)

func sslInfoUnitInstalled(t *testing.T) (*core.Engine, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "sslinfo"}})
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	return e, view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "sslinfo"}))
}

func TestSSLInfoProviderIdentity(t *testing.T) {
	e, ext := sslInfoUnitInstalled(t)
	view := e.CatalogRead()
	control := extension.StockSSLInfo.Control()
	if control.Version != "1.2" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "information about SSL certificates" || ext.Version != "1.2" || len(ext.Members) != 7 {
		t.Fatal("installation/control", control, ext)
	}
	versions := extension.StockSSLInfo.Versions()
	if !slices.Equal(versions.Bases, []string{"1.2"}) || len(versions.Edges) != 0 || extension.ValidateActions(extension.StockSSLInfo) != nil {
		t.Fatal("current-only graph", versions)
	}
	versions.Bases[0] = "changed"
	if extension.StockSSLInfo.Versions().Bases[0] != "1.2" {
		t.Fatal("mutable version backing")
	}
	// Independent released script subset, not expectations copied from member data.
	names := []string{"ssl_client_serial", "ssl_is_used", "ssl_version", "ssl_cipher", "ssl_client_cert_present", "ssl_client_dn", "ssl_issuer_dn"}
	results := []ids.OID{1700, 16, 25, 25, 16, 25, 25}
	for slot, address := range ext.Members {
		p := view.MustProcedure(address)
		if p.Name() != names[slot] || p.Signature != "" || p.Result != results[slot] || p.Volatility != expr.Volatile || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Owner != ext.Owner || p.Namespace() != ext.TargetNamespace || p.Origin.Declaration != slot || p.Origin.Installation != ext.Installation || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'r'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: names[slot], Binary: "$libdir/sslinfo", HasBinary: true}) {
			t.Fatal("released declaration", slot, p)
		}
		if symbol, err := extension.ProcedureSymbol(view, p); err != nil || symbol != names[slot] {
			t.Fatal("sealed symbol", symbol, err)
		}
		if err := extension.StockSSLInfo.ValidateProcedure(view, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := extension.StockSSLInfo.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ssl_client_dn_field", "ssl_issuer_field", "ssl_extension_info"} {
		for p := range catalog.DefinitionProceduresNamedAt(view, nil, ext.TargetNamespace, name) {
			t.Fatal("adjacent declaration installed", p)
		}
	}
	original := view.MustProcedure(ext.Members[1])
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = &extension.SSLInfoProvider{} },
		func(p *catalog.Procedure) { p.Implementation = extension.StockUUIDOssp },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { copy := *p.Origin.Installation; p.Origin.Installation = &copy },
		func(p *catalog.Procedure) { p.Origin.Declaration = 0 },
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
		func(p *catalog.Procedure) { p.Display.Binary = "$libdir/uuid-ossp" },
		func(p *catalog.Procedure) { p.Display.Source = "ssl_version" },
	} {
		bad := *original
		origin := *bad.Origin
		bad.Origin = &origin
		mutate(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		current := view.WithState(state)
		if err := extension.StockSSLInfo.ValidateProcedure(current, &bad); err == nil {
			t.Fatal("forged procedure admitted", bad)
		}
	}
	// Relabel, relocate and detach without replacing immutable release authority.
	state := catalog.CloneState(view.State())
	changed := *original
	changed.ObjectName = "renamed_ssl_inquiry"
	changed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	changed.Extension = ids.ObjectAddress{}
	parent := *ext
	parent.Members = slices.Delete(slices.Clone(ext.Members), 1, 2)
	state.SetDescriptor(parent.Address(), &parent)
	state.UnbindName(catalog.DescriptorNameKey(original))
	state.SetDescriptor(changed.Address(), &changed)
	state.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	current := view.WithState(state)
	if symbol, err := extension.ProcedureSymbol(current, &changed); err != nil || symbol != "ssl_is_used" {
		t.Fatal("current SQL identity selected symbol", symbol, err)
	}
	if err := extension.StockSSLInfo.ValidateExtension(current, &parent); err != nil {
		t.Fatal("lawful membership change", err)
	}
	if view.Get(original.Address()) != original || len(ext.Members) != 7 {
		t.Fatal("immutable source changed")
	}
	// Early and late signature collisions leave the published root untouched,
	// even when earlier declarations were successfully added to the private stage.
	for _, collision := range []int{0, 6} {
		base := catalog.CloneState(view.State())
		for slot, address := range ext.Members {
			if slot != collision {
				p := view.MustProcedure(address)
				base.UnbindName(catalog.DescriptorNameKey(p))
				base.RemoveDescriptor(address)
			}
		}
		before := e.Catalog.State
		stage := e.BeginCatalogStage(view.WithState(base))
		copy := *ext
		e.Mu.Lock()
		err := extension.StockSSLInfo.Stage(&schemacmds.ExtensionHost{Engine: e, Stage: stage}, &copy, ext.Owner)
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		if code, ok := err.(interface{ SQLState() string }); !ok || code.SQLState() != "42723" {
			t.Fatal("signature collision", collision, err)
		}
		if e.Catalog.State != before || before.Get(original.Address()) != original || before.Get(ext.Address()) != ext {
			t.Fatal("partial installation published")
		}
	}
}
