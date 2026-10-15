package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func metaphoneUnitExpression(t *testing.T, e *Engine) installedMetaphoneFunctionExpression {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "gumbo"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 4}},
	}
	node, err := binder.bindInstalledMetaphone(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "metaphone"}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(installedMetaphoneFunctionExpression)
}

func TestMetaphoneExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	n := metaphoneUnitExpression(t, e)
	ctx := &EvalContext{Context: context.Background()}
	for _, tc := range []struct {
		name, want string
		member     int
	}{{"dmetaphone", "SM0", 6}, {"dmetaphone_alt", "XMT", 7}} {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		node, err := binder.bindInstalledMetaphone(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Smith"}}}})
		if err != nil {
			t.Fatal(err)
		}
		unary := node.(installedMetaphoneFunctionExpression)
		if unary.ref.Address != ext.members[tc.member] {
			t.Fatal("unary procedure identity", unary.ref)
		}
		if err := unary.validate(e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		value, err := unary.Eval(ctx)
		code, readErr := value.Text()
		if err != nil || readErr != nil || code != tc.want {
			t.Fatal(tc, value, err, readErr)
		}
	}
	value, err := n.Eval(ctx)
	integer, readErr := value.Text()
	if err != nil || readErr != nil || integer != "KM" || n.ref.Address != ext.members[5] || value.Type() != n.ResultUsage().Type || plannedVolatility(n) != Immutable || !generationImmutable(n) {
		t.Fatal(value, n.ref, err, readErr)
	}
	if err := n.validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	nonmember := *n.procedure
	nonmember.address = ObjectAddress{ClassID: ProcedureClassID, ObjectID: 999999}
	foreignState := cloneCatalog(e.state)
	foreignState.descriptors[nonmember.address] = &nonmember
	if err := stockFuzzystrmatch.validateProcedure(CatalogRead{owner: e, state: foreignState}, &nonmember); err == nil {
		t.Fatal("nonmember procedure authority admitted")
	}
	refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ObjectAddress{n.ref.Address}) {
		t.Fatal(refs, err)
	}
	var children []BoundExpr
	boundExpressionChildren(n, func(child BoundExpr) { children = append(children, child) })
	if !reflect.DeepEqual(children, n.Args) || !sameGroupExpression(n, n) {
		t.Fatal(children)
	}
	changed := n
	changed.Args = append([]BoundExpr(nil), n.Args...)
	changed.Args[0] = literalExpression{expressionInfo: boundExpressionInfo(n.Args[0]), value: sqltype.String(n.Args[0].ResultUsage().Type, "Smith")}
	if sameGroupExpression(n, changed) {
		t.Fatal("argument identity collapsed")
	}
	consumer := &relationDescriptor{generations: map[int32]columnGeneration{1: {kind: generationStored, expression: n, planned: n, dependencies: []ObjectRef{n.ref}}}}
	if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationPlan(e.CatalogRead(), consumer, n); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*procedureDescriptor){
		func(p *procedureDescriptor) { p.result = 23 },
		func(p *procedureDescriptor) { p.signature = "25,25" },
		func(p *procedureDescriptor) { p.name = "dmetaphone" },
		func(p *procedureDescriptor) { p.fuzzy = &fuzzystrmatchProvider{version: "1.2"} },
		func(p *procedureDescriptor) { p.crypto = stockPgcrypto },
	} {
		state := cloneCatalog(e.state)
		bad := *n.procedure
		mutate(&bad)
		state.descriptors[n.ref.Address] = &bad
		forged := n
		forged.procedure = &bad
		if err := forged.validate(CatalogRead{owner: e, state: state}); err == nil {
			t.Fatal("forged installed call admitted", bad)
		}
	}
	for _, foreign := range []bool{false, true} {
		stale := n
		if foreign {
			stale.ref.owner = New().owner
		} else {
			stale.ref.Epoch = e.CatalogRead().Epoch() + 1
		}
		if _, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), stale); err == nil {
			t.Fatal("foreign/stale stored reference admitted")
		}
	}
	retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := retagged.(installedMetaphoneFunctionExpression).validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	folded, _, err := simplifyConditional(ctx, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := folded.(literalExpression); !ok {
		t.Fatal("immutable call not folded", folded)
	}
	// The symbolic codec rejects incomplete old one-member records, as well as
	// duplicate/foreign members in a complete eight-member manifest.
	metadata, err := extensionOwnerMetadata(e.CatalogRead(), e.roleRead(e.CatalogRead(), e.roles), ext)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "-- " + extensionOwnersMarker + " "
	payload := strings.TrimSpace(strings.TrimPrefix(metadata, prefix))
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var record extensionOwnerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"old", "duplicate", "foreign_signature", "foreign_name"} {
		bad := record
		bad.Members = append([]extensionMemberOwner(nil), record.Members...)
		switch kind {
		case "old":
			bad.Members = bad.Members[:1]
		case "duplicate":
			bad.Members[1] = bad.Members[0]
		case "foreign_signature":
			bad.Members[1].Signature = "25"
		case "foreign_name":
			bad.Members[1].Name = "soundex"
		}
		encoded, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := decodeExtensionOwners(" " + extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString(encoded)); err == nil {
			t.Fatal("invalid symbolic inventory admitted", kind)
		}
	}
}

func TestMetaphoneRemap(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	call := metaphoneUnitExpression(t, e)
	before := e.CatalogRead()
	mapping := map[ObjectAddress]ObjectAddress{ext.address: {ClassID: ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.members {
		mapping[address] = ObjectAddress{ClassID: ProcedureClassID, ObjectID: OID(900001 + i)}
	}
	next := cloneCatalog(before.state)
	for address := range mapping {
		delete(next.names, descriptorNameKey(before.state.descriptors[address]))
		delete(next.descriptors, address)
	}
	for address, destination := range mapping {
		descriptor := remapExtensionDescriptor(before.state.descriptors[address], mapping)
		next.descriptors[destination] = descriptor
		next.names[descriptorNameKey(descriptor)] = destination
	}
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 900010}
	consumer := &relationDescriptor{address: address, defaults: map[int32]columnDefault{1: {expression: call, dependencies: []ObjectRef{call.ref}}}, generations: map[int32]columnGeneration{2: {kind: generationStored, expression: call, planned: call, dependencies: []ObjectRef{call.ref}}}}
	next.descriptors[address] = consumer
	next.epoch++
	if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
		t.Fatal(err)
	}
	view := CatalogRead{owner: e, state: next}
	relocated := next.descriptors[address].(*relationDescriptor)
	for _, node := range []BoundExpr{relocated.defaults[1].expression, relocated.generations[2].expression, relocated.generations[2].planned} {
		n := node.(installedMetaphoneFunctionExpression)
		if n.ref.Address != mapping[call.ref.Address] || n.ref.Epoch != next.epoch || n.procedure != next.descriptors[n.ref.Address] {
			t.Fatal("stale remapped authority", n.ref)
		}
		if err := n.validate(view); err != nil {
			t.Fatal(err)
		}
		value, err := n.Eval(&EvalContext{Context: context.Background()})
		number, readErr := value.Text()
		if err != nil || readErr != nil || number != "KM" || value.Type() != n.ResultUsage().Type {
			t.Fatal(value, err, readErr)
		}
	}
	if call.ref.Epoch != before.Epoch() || call.procedure != before.state.descriptors[call.ref.Address] || !reflect.DeepEqual(call.Args, metaphoneUnitExpression(t, e).Args) {
		t.Fatal("source expression mutated")
	}
}
