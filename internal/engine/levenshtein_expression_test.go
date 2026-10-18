package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func levenshteinUnitExpression(t *testing.T, e *Engine) installedLevenshteinFunctionExpression {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "kitten"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "sitting"}},
	}
	for _, n := range []int64{2, 3, 4, 20} {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}})
	}
	node, err := binder.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "levenshtein_less_equal"}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(installedLevenshteinFunctionExpression)
}

func TestLevenshteinExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	n := levenshteinUnitExpression(t, e)
	ctx := &EvalContext{Context: context.Background()}
	value, err := n.Eval(ctx)
	integer, readErr := value.Integer()
	if err != nil || readErr != nil || integer != 10 || n.ref.Address != ext.members[4] || value.Type() != n.ResultUsage().Type || plannedVolatility(n) != Immutable || !generationImmutable(n) {
		t.Fatal(value, n.ref, err, readErr)
	}
	if err := n.validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	nonmember := *n.procedure
	nonmember.address = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999}
	foreignState := cloneCatalog(e.catalog.state)
	foreignState.descriptors[nonmember.address] = &nonmember
	if err := stockFuzzystrmatch.validateProcedure(e.catalog.read(foreignState), &nonmember); err == nil {
		t.Fatal("nonmember procedure authority admitted")
	}
	refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
		t.Fatal(refs, err)
	}
	var children []BoundExpr
	boundExpressionChildren(n, func(child BoundExpr) { children = append(children, child) })
	if !reflect.DeepEqual(children, n.Args) || !sameGroupExpression(n, n) {
		t.Fatal(children)
	}
	for i := range n.Args {
		changed := n
		changed.Args = append([]BoundExpr(nil), n.Args...)
		if i < 2 {
			changed.Args[i] = n.Args[1-i]
		} else {
			changed.Args[i] = n.Args[2+(i-1)%4]
		}
		if sameGroupExpression(n, changed) {
			t.Fatal("argument identity collapsed", i)
		}
	}
	consumer := &relationDescriptor{generations: map[int32]columnGeneration{1: {kind: generationStored, expression: n, planned: n, dependencies: []ids.ObjectRef{n.ref}}}}
	if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationPlan(e.CatalogRead(), consumer, n); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*procedureDescriptor){
		func(p *procedureDescriptor) { p.result = 25 },
		func(p *procedureDescriptor) { p.signature = "25,25" },
		func(p *procedureDescriptor) { p.name = "levenshtein" },
		func(p *procedureDescriptor) { p.fuzzy = &fuzzystrmatchProvider{version: "1.2"} },
		func(p *procedureDescriptor) { p.crypto = stockPgcrypto },
	} {
		state := cloneCatalog(e.catalog.state)
		bad := *n.procedure
		mutate(&bad)
		state.descriptors[n.ref.Address] = &bad
		forged := n
		forged.procedure = &bad
		if err := forged.validate(e.catalog.read(state)); err == nil {
			t.Fatal("forged installed call admitted", bad)
		}
	}
	retagged, _, err := retagBoundTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := retagged.(installedLevenshteinFunctionExpression).validate(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	stored, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), retagged)
	if err != nil || !strings.Contains(stored, "levenshtein_less_equal") || !strings.Contains(stored, "sitting") || !strings.Contains(stored, "20") {
		t.Fatal(stored, err)
	}
	folded, _, err := simplifyConditional(ctx, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := folded.(literalExpression); !ok {
		t.Fatal("immutable call not folded", folded)
	}
	// The symbolic codec rejects incomplete old one-member records, as well as
	// duplicate/foreign members in a complete ten-member manifest.
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
			bad.Members[1].Name = "text_soundex"
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

func TestLevenshteinRemap(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	call := levenshteinUnitExpression(t, e)
	before := e.CatalogRead()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.address: {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	next := cloneCatalog(before.state)
	for address := range mapping {
		delete(next.names, descriptorNameKey(before.Get(address)))
		delete(next.descriptors, address)
	}
	for address, destination := range mapping {
		descriptor := remapExtensionDescriptor(before.Get(address), mapping)
		next.descriptors[destination] = descriptor
		next.names[descriptorNameKey(descriptor)] = destination
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900010}
	consumer := &relationDescriptor{address: address, defaults: map[int32]columnDefault{1: {expression: call, dependencies: []ids.ObjectRef{call.ref}}}, generations: map[int32]columnGeneration{2: {kind: generationStored, expression: call, planned: call, dependencies: []ids.ObjectRef{call.ref}}}}
	next.descriptors[address] = consumer
	next.epoch++
	if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
		t.Fatal(err)
	}
	view := e.catalog.read(next)
	relocated := next.MustRelation(address)
	for _, node := range []BoundExpr{relocated.defaults[1].expression, relocated.generations[2].expression, relocated.generations[2].planned} {
		n := node.(installedLevenshteinFunctionExpression)
		if n.ref.Address != mapping[call.ref.Address] || n.ref.Epoch != next.epoch || n.procedure != next.Get(n.ref.Address) {
			t.Fatal("stale remapped authority", n.ref)
		}
		if err := n.validate(view); err != nil {
			t.Fatal(err)
		}
		value, err := n.Eval(&EvalContext{Context: context.Background()})
		number, readErr := value.Integer()
		if err != nil || readErr != nil || number != 10 || value.Type() != n.ResultUsage().Type {
			t.Fatal(value, err, readErr)
		}
	}
	if call.ref.Epoch != before.Epoch() || call.procedure != before.Get(call.ref.Address) || !reflect.DeepEqual(call.Args, levenshteinUnitExpression(t, e).Args) {
		t.Fatal("source expression mutated")
	}
}
