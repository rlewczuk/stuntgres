package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func metaphoneUnitExpression(t *testing.T, e *Engine) plan.InstalledMetaphoneExpr {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "gumbo"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 4}},
	}
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "metaphone"}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledMetaphoneExpr)
}

func TestMetaphoneExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	n := metaphoneUnitExpression(t, e)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, tc := range []struct {
		name, want string
		member     int
	}{{"dmetaphone", "SM0", 6}, {"dmetaphone_alt", "XMT", 7}} {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Smith"}}}})
		if err != nil {
			t.Fatal(err)
		}
		unary := node.(plan.InstalledMetaphoneExpr)
		if unary.Ref.Address != ext.Members[tc.member] {
			t.Fatal("unary procedure identity", unary.Ref)
		}
		if err := stored.ValidateInstalledMetaphone(unary, e.CatalogRead()); err != nil {
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
	if err != nil || readErr != nil || integer != "KM" || n.Ref.Address != ext.Members[5] || value.Type() != n.ResultUsage().Type || exec.PlannedVolatility(n) != expr.Immutable || !plan.GenerationImmutable(n) {
		t.Fatal(value, n.Ref, err, readErr)
	}
	if err := stored.ValidateInstalledMetaphone(n, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	nonmember := *n.Procedure
	nonmember.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999}
	foreignState := catalog.CloneState(e.catalog.state)
	foreignState.SetDescriptor(nonmember.Address(), &nonmember)
	if err := extension.StockFuzzystrmatch.ValidateProcedure(e.catalog.read(foreignState), &nonmember); err == nil {
		t.Fatal("nonmember procedure authority admitted")
	}
	refs, err := stored.ExpressionDependRefs(ctx.Context, e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.Ref.Address}) {
		t.Fatal(refs, err)
	}
	var children []expr.Expr
	plan.ExpressionChildren(n, func(child expr.Expr) { children = append(children, child) })
	if !reflect.DeepEqual(children, n.Args) || !analyze.SameGroupExpression(n, n) {
		t.Fatal(children)
	}
	changed := n
	changed.Args = append([]expr.Expr(nil), n.Args...)
	changed.Args[0] = plan.LiteralExpr{Info: expr.InfoOf(n.Args[0]), Value: sqltype.String(n.Args[0].ResultUsage().Type, "Smith")}
	if analyze.SameGroupExpression(n, changed) {
		t.Fatal("argument identity collapsed")
	}
	consumer := &catalog.Relation{ColumnMeta: catalog.ColumnMetadata{Generations: map[int32]catalog.ColumnGeneration{1: {Kind: catalog.GenerationStored, Expression: n, Planned: n, Dependencies: []ids.ObjectRef{n.Ref}}}}}
	if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil {
		t.Fatal(err)
	}
	if err := validateGenerationPlan(e.CatalogRead(), consumer, n); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "25,25" },
		func(p *catalog.Procedure) { p.ObjectName = "dmetaphone" },
		func(p *catalog.Procedure) {
			p.Implementation = new(*extension.StockFuzzystrmatch)
		},
		func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto },
	} {
		state := catalog.CloneState(e.catalog.state)
		bad := *n.Procedure
		mutate(&bad)
		state.SetDescriptor(n.Ref.Address, &bad)
		forged := n
		forged.Procedure = &bad
		if err := stored.ValidateInstalledMetaphone(forged, e.catalog.read(state)); err == nil {
			t.Fatal("forged installed call admitted", bad)
		}
	}
	for _, foreign := range []bool{false, true} {
		stale := n
		if foreign {
			stale.Ref = New().catalog.Domain.Ref(stale.Ref.Epoch, stale.Ref.Address)
		} else {
			stale.Ref.Epoch = e.CatalogRead().Epoch() + 1
		}
		if _, err := (scalarRenderer{}).SQL(ctx.Context, e.CatalogRead(), stale); err == nil {
			t.Fatal("foreign/stale stored reference admitted")
		}
	}
	retagged, _, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.ValidateInstalledMetaphone(retagged.(plan.InstalledMetaphoneExpr), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	folded, _, err := exec.SimplifyConditional(ctx, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := folded.(plan.LiteralExpr); !ok {
		t.Fatal("immutable call not folded", folded)
	}
	// The symbolic codec rejects incomplete old one-member records, as well as
	// duplicate/foreign members in a complete ten-member manifest.
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.roles.read(e.catalog.owner(), e.CatalogRead(), e.roles.published), ext)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "-- " + extension.OwnersMarker + " "
	payload := strings.TrimSpace(strings.TrimPrefix(metadata, prefix))
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var record extension.OwnerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"old", "duplicate", "foreign_signature", "foreign_name"} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
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
		if _, _, err := extension.DecodeOwners(" " + extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(encoded)); err == nil {
			t.Fatal("invalid symbolic inventory admitted", kind)
		}
	}
}

func TestMetaphoneRemap(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	call := metaphoneUnitExpression(t, e)
	before := e.CatalogRead()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	next := catalog.CloneState(before.State())
	for address := range mapping {
		next.UnbindName(catalog.DescriptorNameKey(before.Get(address)))
		next.RemoveDescriptor(address)
	}
	for address, destination := range mapping {
		descriptor := extension.RemapDescriptor(before.Get(address), mapping)
		next.SetDescriptor(destination, descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), destination)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900010}
	consumer := &catalog.Relation{ObjectAddress: address, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: {Expression: call, Dependencies: []ids.ObjectRef{call.Ref}}}, Generations: map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: call, Planned: call, Dependencies: []ids.ObjectRef{call.Ref}}}}}
	next.SetDescriptor(address, consumer)
	next.SetEpoch(next.Epoch() + 1)
	if err := e.reownCatalogExpressions(next, mapping, false); err != nil {
		t.Fatal(err)
	}
	view := e.catalog.read(next)
	relocated := next.MustRelation(address)
	for _, node := range []expr.Expr{relocated.ColumnMeta.Defaults[1].Expression, relocated.ColumnMeta.Generations[2].Expression, relocated.ColumnMeta.Generations[2].Planned} {
		n := node.(plan.InstalledMetaphoneExpr)
		if n.Ref.Address != mapping[call.Ref.Address] || n.Ref.Epoch != next.Epoch() || n.Procedure != next.Get(n.Ref.Address) {
			t.Fatal("stale remapped authority", n.Ref)
		}
		if err := stored.ValidateInstalledMetaphone(n, view); err != nil {
			t.Fatal(err)
		}
		value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		number, readErr := value.Text()
		if err != nil || readErr != nil || number != "KM" || value.Type() != n.ResultUsage().Type {
			t.Fatal(value, err, readErr)
		}
	}
	if call.Ref.Epoch != before.Epoch() || call.Procedure != before.Get(call.Ref.Address) || !reflect.DeepEqual(call.Args, metaphoneUnitExpression(t, e).Args) {
		t.Fatal("source expression mutated")
	}
}
