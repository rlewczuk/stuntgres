package engine

import (
	"context"
	"encoding/hex"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgcryptoUnitInstalled(t *testing.T) (*Engine, *extensionDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "pgcrypto"}})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	address := e.catalog.state.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgcrypto"}]
	return e, e.catalog.state.descriptors[address].(*extensionDescriptor)
}

func TestPgcryptoProviderIdentity(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	view := e.CatalogRead()
	if ext.version != "1.4" || len(ext.members) != 14 || ext.Namespace() != (ids.ObjectAddress{}) {
		t.Fatal(ext)
	}
	seen := map[ids.ObjectAddress]bool{}
	expected := []struct {
		name       string
		signature  procedureSignature
		result     ids.OID
		volatility Volatility
		strict     bool
	}{
		{"digest", "25,25", 17, Immutable, true}, {"digest", "17,25", 17, Immutable, true},
		{"hmac", "25,25,25", 17, Immutable, true}, {"hmac", "17,17,25", 17, Immutable, true},
		{"crypt", "25,25", 25, Immutable, true}, {"gen_salt", "25", 25, Volatile, true}, {"gen_salt", "25,23", 25, Volatile, true},
		{"encrypt", "17,17,25", 17, Immutable, true}, {"decrypt", "17,17,25", 17, Immutable, true},
		{"encrypt_iv", "17,17,17,25", 17, Immutable, true}, {"decrypt_iv", "17,17,17,25", 17, Immutable, true},
		{"gen_random_bytes", "23", 17, Volatile, true}, {"gen_random_uuid", "", 2950, Volatile, false}, {"fips_mode", "", 16, Volatile, true},
	}
	for i, address := range ext.members {
		proc := view.state.descriptors[address].(*procedureDescriptor)
		want := expected[i]
		if seen[address] || proc.name != want.name || proc.signature != want.signature || proc.owner != ext.owner || proc.namespace != ext.targetNamespace || proc.result != want.result || proc.strict != want.strict || proc.volatility != want.volatility {
			t.Fatal(proc)
		}
		seen[address] = true
		if err := e.catalog.providers[ids.ProcedureClassID].Validate(view, proc); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, edge := range edges {
		if edge.referenced == ext.address && edge.deptype == 'e' {
			count++
			if !seen[edge.dependent] {
				t.Fatal(edge)
			}
		}
	}
	if count != 14 {
		t.Fatal(count)
	}
	for _, mutate := range []func(*procedureDescriptor){
		func(p *procedureDescriptor) { p.crypto = &pgcryptoProvider{version: "1.4"} },
		func(p *procedureDescriptor) { p.provider = stockUnaccent },
		func(p *procedureDescriptor) { p.queryCodec = stockIntarray },
		func(p *procedureDescriptor) { p.fuzzy = stockFuzzystrmatch },
		func(p *procedureDescriptor) { p.name = "crypt" },
		func(p *procedureDescriptor) { p.signature = "25" },
		func(p *procedureDescriptor) { p.result = 25 },
		func(p *procedureDescriptor) { p.revision = 0 },
		func(p *procedureDescriptor) { p.owner = authorization.PrincipalRef{} },
		func(p *procedureDescriptor) { p.dictionary = ext.address },
		func(p *procedureDescriptor) { p.strict = false },
		func(p *procedureDescriptor) { p.volatility = Stable },
	} {
		original := view.state.descriptors[ext.members[0]]
		bad := *original.(*procedureDescriptor)
		mutate(&bad)
		view.state.descriptors[ext.members[0]] = &bad
		failure := e.catalog.providers[ids.ProcedureClassID].Validate(view, &bad)
		manifestFailure := stockPgcrypto.validate(view, ext)
		view.state.descriptors[ext.members[0]] = original
		if failure == nil {
			t.Fatal("invalid provider accepted", bad)
		}
		if manifestFailure == nil {
			t.Fatal("invalid pgcrypto member manifest accepted", bad)
		}
	}
	for _, address := range ext.members[11:] {
		original := view.state.descriptors[address].(*procedureDescriptor)
		for _, mutate := range []func(*procedureDescriptor){
			func(p *procedureDescriptor) { p.strict = !p.strict },
			func(p *procedureDescriptor) { p.result = 25 },
			func(p *procedureDescriptor) { p.signature = "25" },
			func(p *procedureDescriptor) { p.crypto = &pgcryptoProvider{version: "1.4"} },
		} {
			bad := *original
			mutate(&bad)
			view.state.descriptors[address] = &bad
			failure := stockPgcrypto.validate(view, ext)
			view.state.descriptors[address] = original
			if failure == nil {
				t.Fatal("invalid random/FIPS member admitted", bad)
			}
		}
	}
	bad := *ext
	bad.members = slices.Clone(ext.members)
	bad.members[1] = bad.members[0]
	if err := stockPgcrypto.validate(view, &bad); err == nil {
		t.Fatal("duplicate member accepted")
	}
	bad.members = bad.members[:3]
	if err := stockPgcrypto.validate(view, &bad); err == nil {
		t.Fatal("incomplete parent accepted")
	}
}

func TestPgcryptoExpressionIdentity(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "hmac"}}}, Args: []parser.Expr{literal("what do ya want for nothing?"), literal("Jefe"), literal("sha256")}}
	node, err := binder.bindInstalledCrypto(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(installedCryptoFunctionExpression)
	if n.ref.Address != ext.members[2] || plannedVolatility(n) != Immutable || n.ResultUsage().Type.ID() != sqltype.Bytea {
		t.Fatal(n)
	}
	value, err := n.Eval(&EvalContext{Context: context.Background()})
	bytes, _ := value.Binary()
	if err != nil || hex.EncodeToString([]byte(bytes)) != "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" {
		t.Fatal(bytes, err)
	}
	refs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), n)
	if err != nil || !slices.Contains(refs, n.ref.Address) {
		t.Fatal(refs, err)
	}
	// Stored generation manifests retain the real installed routine provider,
	// rather than reducing the call to its extension or output type.
	generated := &relationDescriptor{
		generations: map[int32]columnGeneration{
			1: {kind: generationStored, expression: n, planned: n, dependencies: []ids.ObjectRef{n.ref}},
		},
	}
	if err := validateGenerationProviders(e.CatalogRead(), generated); err != nil {
		t.Fatal("installed generation provider rejected", err)
	}
	if err := validateGenerationPlan(e.CatalogRead(), generated, n); err != nil {
		t.Fatal("installed generation plan rejected", err)
	}
	stale := n.ref
	stale.Epoch = 0
	generated.generations[1] = columnGeneration{dependencies: []ids.ObjectRef{stale}}
	if err := validateGenerationProviders(e.CatalogRead(), generated); err == nil {
		t.Fatal("invalid installed generation epoch accepted")
	}
	var children []BoundExpr
	boundExpressionChildren(n, func(child BoundExpr) { children = append(children, child) })
	if !reflect.DeepEqual(children, n.Args) {
		t.Fatal(children)
	}
	if !sameGroupExpression(n, n) {
		t.Fatal("identity not reflexive")
	}
	swapped := n
	swapped.Args = slices.Clone(n.Args)
	swapped.Args[0], swapped.Args[1] = swapped.Args[1], swapped.Args[0]
	if sameGroupExpression(n, swapped) {
		t.Fatal("data/key identity collapsed")
	}
	retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := retagged.(installedCryptoFunctionExpression).validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	mapped := remapExtensionDescriptor(n.procedure, map[ids.ObjectAddress]ids.ObjectAddress{n.procedure.address: {ClassID: ids.ProcedureClassID, ObjectID: 999999}}).(*procedureDescriptor)
	if mapped.crypto != stockPgcrypto || mapped.signature != n.procedure.signature || mapped.result != 17 || mapped.address == n.procedure.address {
		t.Fatal(mapped)
	}
	// A four-child call must retain IV/type children and its genuine installed
	// procedure through the same generic walk, render, retag and remap seams.
	aesRaw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "encrypt_iv"}}}, Args: []parser.Expr{literal("foo"), literal("0123456"), literal("abcd"), literal("aes")}}
	aesNode, err := binder.bindInstalledCrypto(aesRaw)
	if err != nil {
		t.Fatal(err)
	}
	aesCall := aesNode.(installedCryptoFunctionExpression)
	if aesCall.ref.Address != ext.members[9] || len(aesCall.Args) != 4 || cryptoExpressionSignature(aesCall) != "17,17,17,25" {
		t.Fatal("four-child installed identity", aesCall)
	}
	children = nil
	boundExpressionChildren(aesCall, func(child BoundExpr) { children = append(children, child) })
	if !reflect.DeepEqual(children, aesCall.Args) {
		t.Fatal("lost four-child traversal", children)
	}
	retagged, _, err = retagBoundTypes(e.CatalogRead(), aesCall, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := retagged.(installedCryptoFunctionExpression).validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	aesRefs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), aesCall)
	if err != nil || !slices.Contains(aesRefs, aesCall.ref.Address) {
		t.Fatal(aesRefs, err)
	}
	mappedAES := remapExtensionDescriptor(aesCall.procedure, map[ids.ObjectAddress]ids.ObjectAddress{aesCall.ref.Address: {ClassID: ids.ProcedureClassID, ObjectID: 999998}}).(*procedureDescriptor)
	if mappedAES.crypto != stockPgcrypto || mappedAES.signature != "17,17,17,25" || mappedAES.address == aesCall.ref.Address {
		t.Fatal(mappedAES)
	}
	for _, v := range []struct {
		name   string
		result sqltype.ID
	}{{"gen_random_bytes", sqltype.Bytea}, {"gen_random_uuid", sqltype.UUID}, {"fips_mode", sqltype.Bool}} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: v.name}}}}
		if v.name == "gen_random_bytes" {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 16}}}
		}
		bound, err := binder.bindInstalledCrypto(raw)
		if err != nil {
			t.Fatal(v.name, err)
		}
		call := bound.(installedCryptoFunctionExpression)
		if call.ResultUsage().Type.ID() != v.result || plannedVolatility(call) != Volatile || !closedPgcryptoDefault(call) {
			t.Fatal("random/FIPS properties", call)
		}
		if err := call.validate(e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		var children []BoundExpr
		boundExpressionChildren(call, func(child BoundExpr) { children = append(children, child) })
		if len(children) != len(call.Args) {
			t.Fatal("random/FIPS walk", children)
		}
		retagged, _, err := retagBoundTypes(e.CatalogRead(), call, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil {
			t.Fatal(err)
		}
		actual := retagged.(installedCryptoFunctionExpression)
		if err := actual.validate(e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		if plannedVolatility(actual) != Volatile || !closedPgcryptoDefault(actual) {
			t.Fatal("lost reconstructed volatility")
		}
		rendered, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), actual)
		if err != nil || !strings.Contains(rendered, v.name) || !strings.Contains(rendered, "public") {
			t.Fatal(rendered, err)
		}
		refs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), actual)
		if err != nil || !slices.Contains(refs, call.ref.Address) {
			t.Fatal(refs, err)
		}
		malformed := actual
		malformed.Args = nil
		if v.name == "gen_random_bytes" && malformed.validate(e.CatalogRead()) == nil {
			t.Fatal("missing count admitted")
		}
		if v.name != "gen_random_bytes" {
			malformed.Args = n.Args[:1]
			if malformed.validate(e.CatalogRead()) == nil {
				t.Fatal("extra zero-call argument admitted")
			}
		}
	}
}
