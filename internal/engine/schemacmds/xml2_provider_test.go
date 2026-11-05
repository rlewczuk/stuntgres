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
	"stuntgres/internal/engine/proc"
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
	provider := v.Owner().Provider(ids.ProcedureClassID)
	if provider == nil {
		t.Fatal("installed procedure class provider missing")
	}
	control := extension.StockXML2.Control()
	if control.Version != "1.2" || control.Trusted || !control.Superuser || control.Relocatable || control.Comment == nil || *control.Comment != "XPath querying and XSLT" || len(ext.Members) != 10 {
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
	names := []string{"xml_valid", "xpath_string", "xpath_number", "xpath_bool", "xpath_table", "xpath_nodeset", "xpath_nodeset", "xpath_nodeset", "xpath_list", "xpath_list"}
	results := []ids.OID{16, 25, 700, 16, 2249, 25, 25, 25, 25, 25}
	signatures := []string{"25", "25,25", "25,25", "25,25", "25,25,25,25,25", "25,25,25,25", "25,25,25", "25,25", "25,25,25", "25,25"}
	for slot, address := range ext.Members {
		p := v.MustProcedure(address)
		signature, volatility := signatures[slot], expr.Immutable
		display := catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: names[slot], Binary: "$libdir/pgxml", HasBinary: true}
		declaration := catalog.InstalledRoutineDeclaration{Parallel: 's'}
		if slot == 4 {
			signature, volatility = "25,25,25,25,25", expr.Stable
			declaration.ReturnsSet, declaration.Rows = true, 1000
		}
		if slot == 0 {
			signature = "25"
			volatility = expr.Stable
			display = catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "xml_is_well_formed"}
		}
		kernel := -1
		switch slot {
		case 6, 7:
			kernel = 5
		case 9:
			kernel = 8
		}
		if kernel >= 0 {
			display = catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}
			body, ok := proc.RoutineDeclarationRecipe(p.DeclarationBody)
			if !ok || !body.Return() || body.CallCount() != 1 {
				t.Fatal("wrapper lost actual structural RETURN body", slot)
			}
			_, call := body.CallPart(0)
			if call.Kind != proc.DeclarationExtensionMember || call.Slot != kernel || call.ArgumentCount != len(strings.Split(signatures[kernel], ",")) {
				t.Fatal("wrapper body lost authentic full-arity member", slot, call)
			}
			selected, err := extension.XML2Kernel(v, p)
			if err != nil || selected != v.MustProcedure(ext.Members[kernel]) || selected.Origin.Installation.Cohort != p.Origin.Installation.Cohort {
				t.Fatal("wrapper's real inner identity", slot, selected, err)
			}
			// Actual native SQL-body initialization uses the shared closed
			// declaration resolver, not the provider metadata helper alone.
			resolved, err := extension.DeclarationMember(v, p, call)
			if err != nil || resolved != selected {
				t.Fatal("wrapper's actual declaration source", slot, resolved, err)
			}
			for _, mutate := range []func(*proc.DeclarationPart){
				func(p *proc.DeclarationPart) { p.Kind = proc.DeclarationRoutine },
				func(p *proc.DeclarationPart) { p.Text = "xpath_string" },
				func(p *proc.DeclarationPart) { p.Slot++ },
				func(p *proc.DeclarationPart) { p.Extension = "xml2" },
				func(p *proc.DeclarationPart) { p.Class = uint32(ids.TypeClassID) },
				func(p *proc.DeclarationPart) { p.Arguments[0] = 16 },
				func(p *proc.DeclarationPart) { p.ArgumentCount-- },
			} {
				bad := call
				mutate(&bad)
				if _, err := extension.DeclarationMember(v, p, bad); err == nil {
					t.Fatal("foreign declaration source accepted", slot, bad)
				}
			}
			state := catalog.CloneState(v.State())
			state.RemoveDescriptor(selected.Address())
			if _, err := extension.DeclarationMember(v.WithState(state), p, call); err == nil {
				t.Fatal("missing genuine kernel accepted", slot)
			}
			bad := *selected
			bad.Implementation = &extension.XML2Provider{}
			state = catalog.CloneState(v.State())
			state.SetDescriptor(bad.Address(), &bad)
			if _, err := extension.DeclarationMember(v.WithState(state), p, call); err == nil {
				t.Fatal("forged current kernel accepted", slot)
			}
		} else if p.DeclarationBody != nil {
			t.Fatal("kernel acquired a SQL wrapper body", slot)
		}
		if extension.StockXML2.SupportsRoutineConfiguration(p) != (kernel >= 0) || (&extension.XML2Provider{}).SupportsRoutineConfiguration(p) {
			t.Fatal("configuration capability lost exact installed SQL body", slot)
		}
		// Validate the normalized private candidate through the actual class
		// provider, not just the extension symbol/capability helper. ALTER and
		// replacement admission must preserve this exact boundary.
		configured := *p
		configured.ObjectRevision++
		configured.Configuration = catalog.NewRoutineConfiguration([]string{"client_min_messages=warning"})
		state := catalog.CloneState(v.State())
		state.SetDescriptor(configured.Address(), &configured)
		current := v.WithState(state)
		if err := provider.Validate(current, &configured); (err == nil) != (kernel >= 0) {
			t.Fatal("normalized configuration class admission", slot, err)
		}
		if kernel >= 0 {
			if err := extension.StockXML2.ValidateExtension(current, ext); err != nil {
				t.Fatal("configured wrapper invalidated genuine installation", slot, err)
			}
		}
		if p.Configuration != nil || v.Get(p.Address()) != p {
			t.Fatal("candidate validation changed original declaration", slot)
		}
		var dependencies []ids.ObjectAddress
		extension.StockXML2.VisitProcedureDependencies(v, p, func(a ids.ObjectAddress) bool {
			dependencies = append(dependencies, a)
			return true
		})
		if kernel < 0 && len(dependencies) != 0 || kernel >= 0 && !slices.Equal(dependencies, []ids.ObjectAddress{ext.Members[kernel]}) {
			t.Fatal("normal wrapper dependency", slot, dependencies)
		}
		if p.Name() != names[slot] || p.Signature != catalog.ProcedureSignature(signature) || p.Result != results[slot] || p.Volatility != volatility || !p.Strict || p.RoutineKind != catalog.FunctionRoutine || p.Installed != declaration || p.Display != display || p.Owner != ext.Owner || p.Namespace() != ext.TargetNamespace || p.Origin.Declaration != slot || p.Origin.Installation != ext.Installation {
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
	for _, name := range []string{"xslt_process", "xml_encode_special_chars"} {
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
	table := v.MustProcedure(ext.Members[4])
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
		func(p *catalog.Procedure) { p.Installed.Rows = 0 },
		func(p *catalog.Procedure) { p.Installed.Rows = 1001 },
		func(p *catalog.Procedure) { p.Installed.Defaults = 1 },
		func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "key" },
		func(p *catalog.Procedure) { p.Volatility = expr.Volatile },
		func(p *catalog.Procedure) { p.Signature = "25,25,25,25" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Installed.Parallel = 'r' },
	} {
		bad := *table
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockXML2.ValidateProcedure(v.WithState(state), &bad); err == nil {
			t.Fatal("forged table declaration accepted", bad)
		}
	}
	wrapper := v.MustProcedure(ext.Members[6])
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = &extension.XML2Provider{} },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { copy := *p.Origin.Installation; p.Origin.Installation = &copy },
		func(p *catalog.Procedure) { p.DeclarationBody = nil },
		func(p *catalog.Procedure) { p.DeclarationBody = v.MustProcedure(ext.Members[7]).DeclarationBody },
		func(p *catalog.Procedure) { p.SecurityDefiner = true },
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Result = 16 },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Configuration = catalog.NewRoutineConfiguration([]string{"broken"}) },
	} {
		bad := *wrapper
		origin := *bad.Origin
		bad.Origin = &origin
		bad.Configuration = catalog.NewRoutineConfiguration([]string{"client_min_messages=warning"})
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := extension.StockXML2.ValidateProcedure(v.WithState(state), &bad); err == nil {
			t.Fatal("forged wrapper body accepted")
		}
		if err := provider.Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("configured forged wrapper accepted by class provider")
		}
		if extension.StockXML2.SupportsRoutineConfiguration(&bad) {
			t.Fatal("forged declaration gained configuration capability")
		}
	}
	// Capability classification is not current-catalog authority. These
	// candidates retain the genuine body/cohort and still fail delegated
	// revision, namespace and current name validation.
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.ObjectRevision = 0 },
		func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} },
		func(p *catalog.Procedure) { p.ObjectName = "not_the_current_wrapper" },
	} {
		bad := *wrapper
		bad.Configuration = catalog.NewRoutineConfiguration([]string{"client_min_messages=warning"})
		mutate(&bad)
		if !extension.StockXML2.SupportsRoutineConfiguration(&bad) {
			t.Fatal("current-catalog negative lost genuine body capability")
		}
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := provider.Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("configuration capability bypassed current class validation")
		}
	}
	// Real Stage rollback includes the published table and final set overload.
	for _, slot := range []int{0, 3, 4, 9} {
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
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || diagnostic.Message != "function \""+names[slot]+"\" already exists with same argument types" || !strings.HasSuffix(diagnostic.Context, map[int]string{0: "extension script file \"xml2--1.1.sql\", near line 9", 3: "extension script file \"xml2--1.1.sql\", near line 29", 4: "extension script file \"xml2--1.1.sql\", near line 57", 9: "extension script file \"xml2--1.1.sql\", near line 39"}[slot]) {
			t.Fatal("released collision", slot, err)
		}
		if e.Catalog.State != before || before.Get(original.Address()) != original || before.Get(ext.Address()) != ext {
			t.Fatal("partial publication")
		}
	}
}

func xml2UnitRetainTemporaryCollisions(t *testing.T, target *core.Engine, members []ids.ObjectAddress) {
	t.Helper()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, citextUnitStage(t, target, installer, "citext", namespace.Name()))
	closeInstaller()
	// Replacement reserves numeric coordinates across catalog classes. Prove
	// this real TEMP installation occupies every source routine coordinate.
	view := target.CatalogRead()
	header := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "citext"}))
	if header.TargetNamespace != namespace.Address() {
		t.Fatal("collision provider is not in the real TEMP namespace")
	}
	occupied := make(map[ids.OID]bool)
	for _, descriptor := range view.Descriptors() {
		temporary := descriptor.Namespace() == namespace.Address() || descriptor.Address() == header.Address() ||
			catalog.ExtensionMembership(descriptor) == header.Address()
		if companion, ok := descriptor.(*catalog.Namespace); ok {
			temporary = temporary || companion.Temporary == namespace.Temporary
		}
		if temporary {
			occupied[descriptor.Address().ObjectID] = true
		}
	}
	for _, address := range members {
		if !occupied[address.ObjectID] {
			t.Fatal("TEMP provider did not occupy source routine coordinate", address)
		}
	}
}
