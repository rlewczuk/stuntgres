package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/extension"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
)

func TestUUIDOsspZeroArity(t *testing.T) {
	for _, tc := range []struct {
		signature catalog.ProcedureSignature
		want      int64
	}{{"", 0}, {"25", 1}, {"25,23", 2}, {"25,23,2950,16", 4}} {
		t.Run(string(tc.signature), func(t *testing.T) {
			if got := pgcatalog.ProcedureArity(&catalog.Procedure{Signature: tc.signature}); got != tc.want {
				t.Fatalf("procedureArity(%q)=%d want=%d", tc.signature, got, tc.want)
			}
		})
	}
}

func uuidOsspUnitInstalled(t *testing.T) (*Engine, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "uuid-ossp"}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"})
	return e, e.catalog.state.MustExtension(address)
}

func TestUUIDOsspProviderIdentity(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	if ext.Version != "1.1" || len(ext.Members) != 6 || ext.Namespace() != (ids.ObjectAddress{}) {
		t.Fatal(ext)
	}
	address := ext.Members[0]
	proc := view.MustProcedure(address)
	if proc.Name() != "uuid_generate_v4" || proc.Signature != "" || pgcatalog.ProcedureArity(proc) != 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Builtin != nil || proc.Result != 2950 || proc.Volatility != expr.Volatile || !proc.Strict || proc.Owner != ext.Owner || proc.Namespace() != ext.TargetNamespace {
		t.Fatal(proc)
	}
	if err := e.catalog.Providers[ids.ProcedureClassID].Validate(view, proc); err != nil {
		t.Fatal(err)
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, engineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.Referenced == ext.Address() && edge.DepType == 'e' {
			count++
			if !slices.Contains(ext.Members, edge.Dependent) {
				t.Fatal(edge)
			}
		}
	}
	if count != 6 {
		t.Fatal("membership count", count)
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) {
			p.Implementation = new(*extension.StockUUIDOssp)
		},
		func(p *catalog.Procedure) { p.RoutineKind = catalog.ProcedureRoutine },
		func(p *catalog.Procedure) { p.Builtin = routine.BuiltinRoutine(3432) },
		func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto },
		func(p *catalog.Procedure) { p.Implementation = extension.StockFuzzystrmatch },
		func(p *catalog.Procedure) { p.Implementation = extension.StockUnaccent },
		func(p *catalog.Procedure) { p.Implementation = extension.StockIntarray },
		func(p *catalog.Procedure) { p.Implementation = extension.StockIntagg },
		func(p *catalog.Procedure) { p.Aggregate = &catalog.OwnedAggregate{} },
		func(p *catalog.Procedure) { p.Foreign = &catalog.ForeignProcedure{} },
		func(p *catalog.Procedure) { p.ObjectName = "uuidv4" },
		func(p *catalog.Procedure) { p.Signature = "25" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.ObjectRevision = 0 },
		func(p *catalog.Procedure) { p.Owner = authorization.PrincipalRef{} },
		func(p *catalog.Procedure) { p.Dictionary = ext.Address() },
	} {
		bad := *proc
		mutate(&bad)
		view.State().SetDescriptor(address, &bad)
		failure := e.catalog.Providers[ids.ProcedureClassID].Validate(view, &bad)
		manifest := extension.StockUUIDOssp.ValidateExtension(view, ext)
		view.State().SetDescriptor(address, proc)
		if failure == nil || manifest == nil {
			t.Fatal("corrupt UUID member accepted", bad)
		}
	}
	for _, mutate := range []func(*catalog.Extension){
		func(x *catalog.Extension) { x.Version = "missing" },
		func(x *catalog.Extension) { x.ObjectName = "pgcrypto" },
		func(x *catalog.Extension) { x.Members = nil },
		func(x *catalog.Extension) { x.Members = []ids.ObjectAddress{address, address} },
	} {
		bad := *ext
		mutate(&bad)
		if err := extension.StockUUIDOssp.ValidateExtension(view, &bad); err == nil {
			t.Fatal("invalid parent accepted")
		}
	}
	// Existing providers must reject a mixed uuid-ossp authority marker too.
	cryptoEngine, cryptoExt := pgcryptoUnitInstalled(t)
	cryptoView := cryptoEngine.CatalogRead()
	original := cryptoView.MustProcedure(cryptoExt.Members[0])
	bad := *original
	bad.Implementation = extension.StockUUIDOssp
	cryptoView.State().SetDescriptor(bad.Address(), &bad)
	failure := extension.StockPgcrypto.ValidateExtension(cryptoView, cryptoExt)
	cryptoView.State().SetDescriptor(bad.Address(), original)
	if failure == nil {
		t.Fatal("mixed existing provider accepted")
	}
	intaggEngine, intaggExt := intaggUnitInstalled(t)
	intaggView := intaggEngine.CatalogRead()
	for _, address := range intaggExt.Members {
		original := intaggView.MustProcedure(address)
		bad := *original
		bad.Implementation = extension.StockUUIDOssp
		intaggView.State().SetDescriptor(address, &bad)
		manifestFailure := extension.StockIntagg.ValidateExtension(intaggView, intaggExt)
		admissionFailure := intaggEngine.catalog.Providers[ids.ProcedureClassID].Validate(intaggView, &bad)
		intaggView.State().SetDescriptor(address, original)
		if manifestFailure == nil || admissionFailure == nil {
			t.Fatal("mixed intagg/uuid-ossp authority accepted")
		}
	}
	builtin := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3432})
	mixed := *builtin
	mixed.Implementation = extension.StockUUIDOssp
	if err := e.catalog.Providers[ids.ProcedureClassID].Validate(view, &mixed); err == nil {
		t.Fatal("mixed builtin/uuid-ossp authority accepted")
	}
}

func TestUUIDOsspProviderCollision(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	before := e.catalog.state
	member := ext.Members[0]
	procedure := before.Get(member)
	descriptors, names := before.DescriptorCount(), before.NameCount()
	key := catalog.DescriptorNameKey(procedure)
	stage := e.BeginCatalogStage(e.CatalogRead())
	copy := *ext
	e.mu.Lock()
	err := uuidOsspInstaller{extension.StockUUIDOssp}.stage(e, stage, &copy, ext.Owner, AuthorizationView{})
	e.mu.Unlock()
	// CatalogStage is a sparse private overlay, not a copy of its base.
	// Atomicity concerns the published root and its existing identities.
	if err == nil || e.catalog.state != before || e.catalog.state.DescriptorCount() != descriptors || e.catalog.state.NameCount() != names || e.catalog.state.Get(member) != procedure || e.catalog.state.Get(ext.Address()) != ext || e.catalog.state.NameAddress(key) != member {
		t.Fatal("procedure collision was not rejected atomically", err)
	}
	if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42723" {
		t.Fatal("wrong collision diagnostic", err)
	}
}
