package schemacmds_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func levenshteinUnitExpression(t *testing.T, e *core.Engine) plan.InstalledLevenshteinExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "kitten"}},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "sitting"}},
	}
	for _, n := range []int64{2, 3, 4, 20} {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}})
	}
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "levenshtein_less_equal"}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledLevenshteinExpr)
}

func TestLevenshteinExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	n := levenshteinUnitExpression(t, e)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := n.Eval(ctx)
	integer, readErr := value.Integer()
	if err != nil || readErr != nil || integer != 10 || n.Ref.Address != ext.Members[4] || value.Type() != n.ResultUsage().Type || exec.PlannedVolatility(n) != expr.Immutable || !plan.GenerationImmutable(n) {
		t.Fatal(value, n.Ref, err, readErr)
	}
	if err := stored.ValidateInstalledLevenshtein(n, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	renamed := *n.Procedure
	renamed.ObjectName = "relabeled_levenshtein"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	currentState := catalog.CloneState(e.Catalog.State)
	currentState.UnbindName(catalog.DescriptorNameKey(n.Procedure))
	currentState.SetDescriptor(renamed.Address(), &renamed)
	currentState.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
	if err := stored.ValidateInstalledLevenshtein(n, e.Catalog.Read(currentState)); err != nil {
		t.Fatal("genuine relabel rejected", err)
	}
	nonmember := *n.Procedure
	nonmember.ObjectAddress = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999}
	foreignState := catalog.CloneState(e.Catalog.State)
	foreignState.SetDescriptor(nonmember.Address(), &nonmember)
	foreignState.BindName(catalog.DescriptorNameKey(&nonmember), nonmember.Address())
	if err := extension.StockFuzzystrmatch.ValidateProcedure(e.Catalog.Read(foreignState), &nonmember); err == nil {
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
	for i := range n.Args {
		changed := n
		changed.Args = append([]expr.Expr(nil), n.Args...)
		if i < 2 {
			changed.Args[i] = n.Args[1-i]
		} else {
			changed.Args[i] = n.Args[2+(i-1)%4]
		}
		if analyze.SameGroupExpression(n, changed) {
			t.Fatal("argument identity collapsed", i)
		}
	}
	consumer := &catalog.Relation{ColumnMeta: catalog.ColumnMetadata{Generations: map[int32]catalog.ColumnGeneration{1: {Kind: catalog.GenerationStored, Expression: n, Planned: n, Dependencies: []ids.ObjectRef{n.Ref}}}}}
	if err := table.ValidateGenerationProviders(e.CatalogRead(), consumer); err != nil {
		t.Fatal(err)
	}
	if err := table.ValidateGenerationPlan(e.CatalogRead(), consumer, n); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Signature = "25,25" },
		func(p *catalog.Procedure) {
			p.Implementation = new(*extension.StockFuzzystrmatch)
		},
		func(p *catalog.Procedure) { p.Implementation = extension.StockPgcrypto },
	} {
		state := catalog.CloneState(e.Catalog.State)
		state.UnbindName(catalog.DescriptorNameKey(n.Procedure))
		bad := *n.Procedure
		mutate(&bad)
		state.SetDescriptor(n.Ref.Address, &bad)
		state.BindName(catalog.DescriptorNameKey(&bad), bad.Address())
		forged := n
		forged.Procedure = &bad
		if err := stored.ValidateInstalledLevenshtein(forged, e.Catalog.Read(state)); err == nil {
			t.Fatal("forged installed call admitted", bad)
		}
	}
	for _, foreign := range []bool{false, true} {
		stale := n
		if foreign {
			stale.Ref = engine.New().Catalog.Domain.Ref(stale.Ref.Epoch, stale.Ref.Address)
		} else {
			stale.Ref.Epoch = e.CatalogRead().Epoch() + 1
		}
		if _, err := (stored.ScalarRenderer{}).SQL(ctx.Context, e.CatalogRead(), stale); err == nil {
			t.Fatal("foreign/stale stored reference admitted")
		}
	}
	retagged, _, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.ValidateInstalledLevenshtein(retagged.(plan.InstalledLevenshteinExpr), e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(ctx.Context, e.CatalogRead(), retagged); err != nil {
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
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.RoleStore.PublishedRead(&e.Catalog.Owner, e.CatalogRead()), ext)
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
	relabeled := record
	relabeled.Members = append([]extension.MemberOwner(nil), record.Members...)
	relabeled.Members[1].Name = "text_soundex"
	encoded, err := json.Marshal(relabeled)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := extension.DecodeOwners(" " + extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(encoded)); err != nil || !found {
		t.Fatal("genuine symbolic relabel rejected", err)
	}
	for _, kind := range []string{"old", "duplicate", "foreign_signature", "missing_schema"} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		switch kind {
		case "old":
			bad.Members = bad.Members[:1]
		case "duplicate":
			bad.Members[1] = bad.Members[0]
		case "foreign_signature":
			bad.Members[1].Signature = "25"
		case "missing_schema":
			bad.Members[1].Schema = ""
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

func TestLevenshteinRemap(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	call := levenshteinUnitExpression(t, e)
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
	if err := stored.ReownExpressions(e.Catalog.Read(next), next, mapping, false, stored.NewRoutineRemap(e.Catalog.Read(next), mapping, nil, extension.ProcedureRemapper(mapping, nil, nil), false)); err != nil {
		t.Fatal(err)
	}
	view := e.Catalog.Read(next)
	relocated := next.MustRelation(address)
	for _, node := range []expr.Expr{relocated.ColumnMeta.Defaults[1].Expression, relocated.ColumnMeta.Generations[2].Expression, relocated.ColumnMeta.Generations[2].Planned} {
		n := node.(plan.InstalledLevenshteinExpr)
		if n.Ref.Address != mapping[call.Ref.Address] || n.Ref.Epoch != next.Epoch() || !exec.SameRoutineDeclaration(next.MustProcedure(n.Ref.Address), n.Procedure) || n.Procedure.Revision() != call.Procedure.Revision() {
			t.Fatal("stale remapped authority", n.Ref)
		}
		if err := stored.ValidateInstalledLevenshtein(n, view); err != nil {
			t.Fatal(err)
		}
		value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		number, readErr := value.Integer()
		if err != nil || readErr != nil || number != 10 || value.Type() != n.ResultUsage().Type {
			t.Fatal(value, err, readErr)
		}
	}
	if call.Ref.Epoch != before.Epoch() || call.Procedure != before.Get(call.Ref.Address) || !reflect.DeepEqual(call.Args, levenshteinUnitExpression(t, e).Args) {
		t.Fatal("source expression mutated")
	}
}
