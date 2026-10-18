package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestUUIDOsspZeroArity(t *testing.T) {
	for _, tc := range []struct {
		signature procedureSignature
		want      int64
	}{{"", 0}, {"25", 1}, {"25,23", 2}, {"25,23,2950,16", 4}} {
		t.Run(string(tc.signature), func(t *testing.T) {
			if got := procedureArity(&procedureDescriptor{signature: tc.signature}); got != tc.want {
				t.Fatalf("procedureArity(%q)=%d want=%d", tc.signature, got, tc.want)
			}
		})
	}
}

func uuidOsspUnitInstalled(t *testing.T) (*Engine, *extensionDescriptor) {
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
	if ext.version != "1.1" || len(ext.members) != 1 || ext.Namespace() != (ids.ObjectAddress{}) {
		t.Fatal(ext)
	}
	address := ext.members[0]
	proc := view.MustProcedure(address)
	if proc.name != "uuid_generate_v4" || proc.signature != "" || procedureArity(proc) != 0 || proc.routineKind != functionRoutine || proc.builtin != nil || proc.result != 2950 || proc.volatility != expr.Volatile || !proc.strict || proc.owner != ext.owner || proc.namespace != ext.targetNamespace {
		t.Fatal(proc)
	}
	if err := e.catalog.providers[ids.ProcedureClassID].Validate(view, proc); err != nil {
		t.Fatal(err)
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.referenced == ext.address && edge.deptype == 'e' {
			count++
			if edge.dependent != address {
				t.Fatal(edge)
			}
		}
	}
	if count != 1 {
		t.Fatal("membership count", count)
	}
	for _, mutate := range []func(*procedureDescriptor){
		func(p *procedureDescriptor) { p.uuidOssp = &uuidOsspProvider{version: "1.1"} },
		func(p *procedureDescriptor) { p.routineKind = procedureRoutine },
		func(p *procedureDescriptor) { p.builtin = builtinRoutineByOID[3432] },
		func(p *procedureDescriptor) { p.crypto = stockPgcrypto },
		func(p *procedureDescriptor) { p.fuzzy = stockFuzzystrmatch },
		func(p *procedureDescriptor) { p.provider = stockUnaccent },
		func(p *procedureDescriptor) { p.queryCodec = stockIntarray },
		func(p *procedureDescriptor) { p.intagg = stockIntagg },
		func(p *procedureDescriptor) { p.aggregate = &ownedAggregate{} },
		func(p *procedureDescriptor) { p.foreign = &foreignProcedure{} },
		func(p *procedureDescriptor) { p.name = "uuidv4" },
		func(p *procedureDescriptor) { p.signature = "25" },
		func(p *procedureDescriptor) { p.result = 25 },
		func(p *procedureDescriptor) { p.volatility = expr.Immutable },
		func(p *procedureDescriptor) { p.strict = false },
		func(p *procedureDescriptor) { p.revision = 0 },
		func(p *procedureDescriptor) { p.owner = authorization.PrincipalRef{} },
		func(p *procedureDescriptor) { p.dictionary = ext.address },
	} {
		bad := *proc
		mutate(&bad)
		view.state.setDescriptor(address, &bad)
		failure := e.catalog.providers[ids.ProcedureClassID].Validate(view, &bad)
		manifest := stockUUIDOssp.validate(view, ext)
		view.state.setDescriptor(address, proc)
		if failure == nil || manifest == nil {
			t.Fatal("corrupt UUID member accepted", bad)
		}
	}
	for _, mutate := range []func(*extensionDescriptor){
		func(x *extensionDescriptor) { x.version = "missing" },
		func(x *extensionDescriptor) { x.name = "pgcrypto" },
		func(x *extensionDescriptor) { x.members = nil },
		func(x *extensionDescriptor) { x.members = []ids.ObjectAddress{address, address} },
	} {
		bad := *ext
		mutate(&bad)
		if err := stockUUIDOssp.validate(view, &bad); err == nil {
			t.Fatal("invalid parent accepted")
		}
	}
	// Existing providers must reject a mixed uuid-ossp authority marker too.
	cryptoEngine, cryptoExt := pgcryptoUnitInstalled(t)
	cryptoView := cryptoEngine.CatalogRead()
	original := cryptoView.MustProcedure(cryptoExt.members[0])
	bad := *original
	bad.uuidOssp = stockUUIDOssp
	cryptoView.state.setDescriptor(bad.address, &bad)
	failure := stockPgcrypto.validate(cryptoView, cryptoExt)
	cryptoView.state.setDescriptor(bad.address, original)
	if failure == nil {
		t.Fatal("mixed existing provider accepted")
	}
	intaggEngine, intaggExt := intaggUnitInstalled(t)
	intaggView := intaggEngine.CatalogRead()
	for _, address := range intaggExt.members {
		original := intaggView.MustProcedure(address)
		bad := *original
		bad.uuidOssp = stockUUIDOssp
		intaggView.state.setDescriptor(address, &bad)
		manifestFailure := stockIntagg.validate(intaggView, intaggExt)
		admissionFailure := intaggEngine.catalog.providers[ids.ProcedureClassID].Validate(intaggView, &bad)
		intaggView.state.setDescriptor(address, original)
		if manifestFailure == nil || admissionFailure == nil {
			t.Fatal("mixed intagg/uuid-ossp authority accepted")
		}
	}
	builtin := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3432})
	mixed := *builtin
	mixed.uuidOssp = stockUUIDOssp
	if err := e.catalog.providers[ids.ProcedureClassID].Validate(view, &mixed); err == nil {
		t.Fatal("mixed builtin/uuid-ossp authority accepted")
	}
}

func TestUUIDOsspProviderCollision(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	before := e.catalog.state
	member := ext.members[0]
	procedure := before.Get(member)
	descriptors, names := before.DescriptorCount(), before.NameCount()
	key := descriptorNameKey(procedure)
	stage := e.BeginCatalogStage(e.CatalogRead())
	copy := *ext
	e.mu.Lock()
	err := stockUUIDOssp.stage(e, stage, &copy, ext.owner, AuthorizationView{})
	e.mu.Unlock()
	// CatalogStage is a sparse private overlay, not a copy of its base.
	// Atomicity concerns the published root and its existing identities.
	if err == nil || e.catalog.state != before || e.catalog.state.DescriptorCount() != descriptors || e.catalog.state.NameCount() != names || e.catalog.state.Get(member) != procedure || e.catalog.state.Get(ext.address) != ext || e.catalog.state.NameAddress(key) != member {
		t.Fatal("procedure collision was not rejected atomically", err)
	}
	if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42723" {
		t.Fatal("wrong collision diagnostic", err)
	}
}
