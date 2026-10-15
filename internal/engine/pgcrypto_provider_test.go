package engine

import (
	"context"
	"encoding/hex"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
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
	address := e.state.names[NameKey{ClassID: ExtensionClassID, Name: "pgcrypto"}]
	return e, e.state.descriptors[address].(*extensionDescriptor)
}

func TestPgcryptoProviderIdentity(t *testing.T) {
	e, ext := pgcryptoUnitInstalled(t)
	view := e.CatalogRead()
	if ext.version != "1.4" || len(ext.members) != 11 || ext.Namespace() != (ObjectAddress{}) {
		t.Fatal(ext)
	}
	seen := map[ObjectAddress]bool{}
	expected := []struct {
		name       string
		signature  procedureSignature
		result     OID
		volatility Volatility
	}{
		{"digest", "25,25", 17, Immutable}, {"digest", "17,25", 17, Immutable},
		{"hmac", "25,25,25", 17, Immutable}, {"hmac", "17,17,25", 17, Immutable},
		{"crypt", "25,25", 25, Immutable}, {"gen_salt", "25", 25, Volatile}, {"gen_salt", "25,23", 25, Volatile},
		{"encrypt", "17,17,25", 17, Immutable}, {"decrypt", "17,17,25", 17, Immutable},
		{"encrypt_iv", "17,17,17,25", 17, Immutable}, {"decrypt_iv", "17,17,17,25", 17, Immutable},
	}
	for i, address := range ext.members {
		proc := view.state.descriptors[address].(*procedureDescriptor)
		want := expected[i]
		if seen[address] || proc.name != want.name || proc.signature != want.signature || proc.owner != ext.owner || proc.namespace != ext.targetNamespace || proc.result != want.result || !proc.strict || proc.volatility != want.volatility {
			t.Fatal(proc)
		}
		seen[address] = true
		if err := e.providers[ProcedureClassID].Validate(view, proc); err != nil {
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
	if count != 11 {
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
		failure := e.providers[ProcedureClassID].Validate(view, &bad)
		manifestFailure := stockPgcrypto.validate(view, ext)
		view.state.descriptors[ext.members[0]] = original
		if failure == nil {
			t.Fatal("invalid provider accepted", bad)
		}
		if manifestFailure == nil {
			t.Fatal("invalid pgcrypto member manifest accepted", bad)
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
			1: {kind: generationStored, expression: n, planned: n, dependencies: []ObjectRef{n.ref}},
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
	generated.generations[1] = columnGeneration{dependencies: []ObjectRef{stale}}
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
	mapped := remapExtensionDescriptor(n.procedure, map[ObjectAddress]ObjectAddress{n.procedure.address: {ClassID: ProcedureClassID, ObjectID: 999999}}).(*procedureDescriptor)
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
	mappedAES := remapExtensionDescriptor(aesCall.procedure, map[ObjectAddress]ObjectAddress{aesCall.ref.Address: {ClassID: ProcedureClassID, ObjectID: 999998}}).(*procedureDescriptor)
	if mappedAES.crypto != stockPgcrypto || mappedAES.signature != "17,17,17,25" || mappedAES.address == aesCall.ref.Address {
		t.Fatal(mappedAES)
	}
}
