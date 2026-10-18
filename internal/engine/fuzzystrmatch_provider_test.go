package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func daitchUnitInstalled(t *testing.T) (*Engine, *extensionDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "fuzzystrmatch", ""))
	return e, e.catalog.state.descriptors[e.catalog.state.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}]].(*extensionDescriptor)
}

func daitchUnitExpression(t *testing.T, e *Engine, input string) installedFunctionExpression {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "daitch_mokotoff"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}}}
	node, err := b.bindInstalledFunction(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(installedFunctionExpression)
}

func TestDaitchMokotoffProviderIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	view := e.CatalogRead()
	if len(ext.members) != 10 {
		t.Fatal("member count", ext.members)
	}
	proc := view.state.descriptors[ext.members[0]].(*procedureDescriptor)
	if proc.namespace != ext.targetNamespace || proc.extension != ext.address || proc.owner != ext.owner || proc.result != 1009 || proc.signature != "25" || proc.volatility != Immutable || !proc.strict || proc.fuzzy != stockFuzzystrmatch {
		t.Fatal("installed identity", proc)
	}
	expected := map[string]ids.OID{"daitch_mokotoff:25": 1009, "levenshtein:25,25": 23, "levenshtein:25,25,23,23,23": 23, "levenshtein_less_equal:25,25,23": 23, "levenshtein_less_equal:25,25,23,23,23,23": 23, "metaphone:25,23": 25, "dmetaphone:25": 25, "dmetaphone_alt:25": 25, "soundex:25": 25, "difference:25,25": 23}
	addresses := map[ids.ObjectAddress]bool{}
	for _, address := range ext.members {
		member := view.state.descriptors[address].(*procedureDescriptor)
		key := member.name + ":" + string(member.signature)
		if expected[key] == 0 || member.result != expected[key] || addresses[address] || member.owner != proc.owner || member.namespace != ext.targetNamespace || member.extension != ext.address || member.volatility != Immutable || !member.strict || member.fuzzy != stockFuzzystrmatch {
			t.Fatal("selected member identity", member)
		}
		delete(expected, key)
		addresses[address] = true
		if err := (extensionCatalogProvider{ids.ProcedureClassID}).Validate(view, member); err != nil {
			t.Fatal(err)
		}
	}
	if len(expected) != 0 {
		t.Fatal("missing selected members", expected)
	}
	for _, d := range []Descriptor{ext, proc} {
		if err := (extensionCatalogProvider{d.Address().ClassID}).Validate(view, d); err != nil {
			t.Fatal(err)
		}
	}
	edges, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	members := 0
	for _, edge := range edges {
		if edge.referenced == ext.address && edge.deptype == 'e' {
			if !addresses[edge.dependent] {
				t.Fatal("foreign member edge")
			}
			members++
		}
	}
	if members != 10 {
		t.Fatal("membership edges", members)
	}
	for _, mutate := range []func(*procedureDescriptor){
		func(p *procedureDescriptor) { p.name = "text_soundex" }, func(p *procedureDescriptor) { p.signature = "23" },
		func(p *procedureDescriptor) { p.result = 25 }, func(p *procedureDescriptor) { p.volatility = Stable },
		func(p *procedureDescriptor) { p.strict = false }, func(p *procedureDescriptor) { p.provider = stockUnaccent },
		func(p *procedureDescriptor) { p.queryCodec = stockIntarray }, func(p *procedureDescriptor) { p.fuzzy = &fuzzystrmatchProvider{version: "1.2"} },
		func(p *procedureDescriptor) { p.namespace = ids.ObjectAddress{} }, func(p *procedureDescriptor) { p.extension = ids.ObjectAddress{} },
		func(p *procedureDescriptor) { p.dictionary = ext.address }, func(p *procedureDescriptor) { p.revision = 0 },
	} {
		state := cloneCatalog(e.catalog.state)
		bad := *proc
		mutate(&bad)
		state.descriptors[proc.address] = &bad
		if err := stockFuzzystrmatch.validate(CatalogRead{owner: e, state: state}, ext); err == nil {
			t.Fatal("invalid procedure admitted", bad)
		}
	}
	for _, members := range [][]ids.ObjectAddress{nil, {proc.address, proc.address}, {{ClassID: ids.TypeClassID, ObjectID: proc.address.ObjectID}}} {
		bad := *ext
		bad.members = members
		if err := stockFuzzystrmatch.validate(view, &bad); err == nil {
			t.Fatal("invalid membership admitted", members)
		}
	}
	duplicate := *ext
	duplicate.members = append([]ids.ObjectAddress(nil), ext.members...)
	duplicate.members[4] = duplicate.members[1]
	if err := stockFuzzystrmatch.validate(view, &duplicate); err == nil {
		t.Fatal("full-size duplicate inventory admitted")
	}
	metadata, err := extensionOwnerMetadata(view, e.roleRead(view, e.roles), ext)
	if err != nil {
		t.Fatal(err)
	}
	var records []extensionOwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION fuzzystrmatch;\n"+metadata, parser.Options{Context: context.Background(), StandardConformingStrings: true, LineComment: func(comment string) error {
		record, found, err := decodeExtensionOwners(comment)
		if found && err == nil {
			records = append(records, record)
		}
		return err
	}})
	if err != nil || len(records) != 1 || len(records[0].Members) != 10 || records[0].Members[0].Name != "daitch_mokotoff" || records[0].Members[0].Signature != "25" {
		t.Fatal("symbolic ownership", records, err)
	}
	symbolic := map[string]bool{"daitch_mokotoff:25": true, "levenshtein:25,25": true, "levenshtein:25,25,23,23,23": true, "levenshtein_less_equal:25,25,23": true, "levenshtein_less_equal:25,25,23,23,23,23": true, "metaphone:25,23": true, "dmetaphone:25": true, "dmetaphone_alt:25": true, "soundex:25": true, "difference:25,25": true}
	for _, member := range records[0].Members {
		key := member.Name + ":" + member.Signature
		if member.Kind != "procedure" || member.Owner != "postgres" || !symbolic[key] {
			t.Fatal("symbolic member identity", member)
		}
		delete(symbolic, key)
	}
	if len(symbolic) != 0 {
		t.Fatal("missing symbolic selected members", symbolic)
	}
	for _, payload := range []string{
		`{"name":"fuzzystrmatch","schema":"public","version":"1.1","owner":"postgres","members":[]}`,
		`{"name":"fuzzystrmatch","schema":"public","version":"1.2","owner":"postgres","members":[{"kind":"procedure","name":"text_soundex","signature":"25","owner":"postgres"}]}`,
		`{"name":"fuzzystrmatch","schema":"public","version":"1.2","owner":"postgres","members":[]}`,
	} {
		if _, _, err := decodeExtensionOwners(" " + extensionOwnersMarker + " " + base64.StdEncoding.EncodeToString([]byte(payload))); err == nil {
			t.Fatal("foreign owner manifest accepted", payload)
		}
	}
	t.Run("canceled_stage_does_not_publish", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		before := e.catalog.state
		stage := intarrayUnitStage(t, e, op, "fuzzystrmatch", "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled validation", err)
		}
		if e.catalog.state != before {
			t.Fatal("canceled stage published")
		}
	})
	t.Run("own_TEMP_has_no_dictionary_helper_restriction", func(t *testing.T) {
		e := New()
		op, closeOwner := extensionTemporaryOperation(t, e, "postgres")
		defer closeOwner()
		ns := intarrayUnitTemporaryNamespace(t, e, op)
		extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "fuzzystrmatch", ns.name))
		ext := e.catalog.state.descriptors[e.catalog.state.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}]].(*extensionDescriptor)
		if err := stockFuzzystrmatch.validate(e.CatalogRead(), ext); err != nil {
			t.Fatal("own TEMP provider", err)
		}
		member := ext.members[0]
		closeOwner()
		if e.catalog.state.descriptors[ext.address] != nil || e.catalog.state.descriptors[member] != nil {
			t.Fatal("TEMP provider survived owner retirement")
		}
		for _, address := range ext.members {
			if e.catalog.state.descriptors[address] != nil {
				t.Fatal("TEMP selected member survived owner retirement", address)
			}
		}
	})
}

type daitchCountingArgument struct {
	expressionInfo
	calls *int
	value sqltype.Value
}

func (a daitchCountingArgument) Eval(ExprContext) (sqltype.Value, error) {
	*a.calls++
	return a.value, nil
}

func TestDaitchMokotoffExpressionIdentity(t *testing.T) {
	e, ext := daitchUnitInstalled(t)
	n := daitchUnitExpression(t, e, "Bierschbach")
	ctx := &EvalContext{Context: context.Background()}
	value, err := n.Eval(ctx)
	text, _ := n.Usage.Type.Output(ctx.Context, value)
	if err != nil || text != "{794575,794574,794750,794740,745750,745740,747500,747400}" || plannedVolatility(n) != Immutable || !generationImmutable(n) {
		t.Fatal("immutable complete evaluation", text, err)
	}
	if n.ref.Address != ext.members[0] || n.dictionary != nil || n.template != nil || value.Type() != n.Usage.Type || value.ArrayLen() != 8 {
		t.Fatal("captured authority", n)
	}
	for i := range value.ArrayLen() {
		if value.ArrayElement(i).Type() != n.Usage.Type.Element() {
			t.Fatal("foreign element generation")
		}
	}
	refs, err := pgDependExpressionRefs(ctx.Context, e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
		t.Fatal("dependency", refs, err)
	}
	consumer := &relationDescriptor{generations: map[int32]columnGeneration{
		1: {expression: n, dependencies: []ids.ObjectRef{n.ref}},
	}}
	if err := validateGenerationProviders(e.CatalogRead(), consumer); err != nil {
		t.Fatal("installed generation procedure dependency", err)
	}
	badRef := n.ref
	badRef.Address.SubID = 1
	invalid := *consumer
	invalid.generations = map[int32]columnGeneration{
		1: {expression: n, dependencies: []ids.ObjectRef{badRef}},
	}
	if err := validateGenerationProviders(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("procedure subobject generation dependency admitted")
	}
	state := cloneCatalog(e.catalog.state)
	badProcedure := *n.procedure
	badProcedure.result = 25
	state.descriptors[n.ref.Address] = &badProcedure
	if err := validateGenerationProviders(CatalogRead{owner: e, state: state}, consumer); err == nil {
		t.Fatal("corrupt installed generation manifest admitted")
	}
	children := 0
	boundExpressionChildren(n, func(BoundExpr) { children++ })
	if children != 1 || !sameGroupExpression(n, n) {
		t.Fatal("walker/group identity", children)
	}
	stored, err := (scalarRenderer{}).sql(ctx.Context, e.CatalogRead(), n)
	if err != nil || !strings.Contains(stored, "daitch_mokotoff") {
		t.Fatal("stored renderer", stored, err)
	}
	folded, _, err := simplifyConditional(ctx, n, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := folded.(literalExpression); !ok {
		t.Fatal("immutable literal function not folded", folded)
	}
	for _, input := range []string{"", "---"} {
		v, err := daitchUnitExpression(t, e, input).Eval(ctx)
		if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
			t.Fatal("typed no-encodable NULL", v, err)
		}
	}
	foreign := New()
	foreignArray, _ := foreign.CatalogRead().LookupType(1009)
	forged := n
	forged.Usage.Type = foreignArray
	if forged.validProvider() {
		t.Fatal("foreign array/argument generation admitted")
	}
	if _, err := forged.Eval(ctx); err == nil {
		t.Fatal("foreign generation evaluated")
	}
	calls := 0
	n.arg = daitchCountingArgument{expressionInfo: expressionInfo{Usage: n.arg.ResultUsage(), Property: ExprProperties{Volatility: Volatile}}, calls: &calls, value: sqltype.Null(n.arg.ResultUsage().Type)}
	v, err := n.Eval(ctx)
	if err != nil || calls != 1 || !v.IsNull() || v.Type() != n.Usage.Type || plannedVolatility(n) != Volatile || generationImmutable(n) {
		t.Fatal("strict single eval/child volatility", calls, v, err)
	}
	ctxCanceled, cancel := context.WithCancel(ctx.Context)
	cancel()
	if _, err := n.Eval(&EvalContext{Context: ctxCanceled}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("precanceled evaluation", calls, err)
	}
	unaccent, _, _ := extensionUnitInstalled(t)
	b := newTestExpressionBinder(t, ctx.Context, unaccent, sqltype.InputEnvironment{}, nil)
	u, err := b.bindInstalledFunction(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}})
	if err != nil || plannedVolatility(u) != Stable || generationImmutable(u) {
		t.Fatal("unaccent remains stable", u, err)
	}
}

func TestDaitchMokotoffRemap(t *testing.T) {
	source, ext := daitchUnitInstalled(t)
	old := daitchUnitExpression(t, source, "John")
	oldValue, err := old.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	sourceView := source.CatalogRead()
	sourceArray, sourceText := oldValue.Type(), oldValue.ArrayElement(0).Type()
	sourceProcedure := old.procedure
	procedureBefore := *sourceProcedure
	sourceRef, sourceUsage := old.ref, old.ResultUsage()
	sourceDimensions := oldValue.ArrayDimensions()
	target := New()
	if _, err := target.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := target.catalog.state.descriptors[target.catalog.state.names[ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}]].(*extensionDescriptor)
	mapped, err := remapBoundExpression(old, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address == ext.members[0] {
			ref.Address = imported.members[0]
		}
		ref = target.catalog.domain.Ref(ref.Epoch, ref.Address)
		ref.Epoch = target.CatalogRead().Epoch()
		return ref
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mapped, changed, err := retagBoundTypes(target.CatalogRead(), mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		owned, ok := target.CatalogRead().LookupType(d.ID())
		if !ok {
			t.Fatal("missing target type", d.ID())
		}
		return owned
	})
	if err != nil || !changed {
		t.Fatal("retag", changed, err)
	}
	n := mapped.(installedFunctionExpression)
	v, err := n.Eval(&EvalContext{Context: context.Background()})
	ownedArray, _ := target.CatalogRead().LookupType(1009)
	ownedText, _ := target.CatalogRead().LookupType(sqltype.Text)
	if err != nil || v.Type() != ownedArray || v.ArrayElement(0).Type() != ownedText || n.procedure != target.catalog.state.descriptors[imported.members[0]] || n.dictionary != nil || n.template != nil {
		t.Fatal("remapped provider/generations", v, err)
	}
	// Foreign import can share immutable builtin TEXT/text[] descriptors.
	// Retention means unchanged source ownership/data, not pointer inequality
	// between the two catalogs' immutable builtin descriptors.
	currentSourceArray, _ := source.CatalogRead().LookupType(1009)
	currentSourceText, _ := source.CatalogRead().LookupType(sqltype.Text)
	if source.CatalogRead().state != sourceView.state || currentSourceArray != sourceArray || currentSourceText != sourceText || sourceArray.Element() != sourceText || oldValue.Type() != sourceArray || oldValue.ArrayElement(0).Type() != sourceText || old.procedure != sourceProcedure || source.catalog.state.descriptors[sourceRef.Address] != sourceProcedure || old.ref != sourceRef || old.ResultUsage() != sourceUsage || !reflect.DeepEqual(*sourceProcedure, procedureBefore) || !reflect.DeepEqual(oldValue.ArrayDimensions(), sourceDimensions) {
		t.Fatal("retained source changed")
	}
	if n.procedure == sourceProcedure || n.ref.Address != n.procedure.address || n.ref.Domain() != target.catalog.domain || n.ref.Epoch != target.CatalogRead().Epoch() || ownedArray.Element() != ownedText {
		t.Fatal("remapped installed authority still belongs to source")
	}
	retainedText, err := sourceArray.Output(context.Background(), oldValue)
	if err != nil || retainedText != "{160000,460000}" {
		t.Fatal("retained array data changed", retainedText, err)
	}
	retainedValue, err := old.Eval(&EvalContext{Context: context.Background()})
	if err != nil || retainedValue.Type() != sourceArray || retainedValue.ArrayElement(0).Type() != sourceText {
		t.Fatal("retained source expression generation changed", retainedValue, err)
	}
	retainedText, err = sourceArray.Output(context.Background(), retainedValue)
	if err != nil || retainedText != "{160000,460000}" {
		t.Fatal("retained source expression data changed", retainedText, err)
	}
	text, err := ownedArray.Output(context.Background(), v)
	if err != nil || text != "{160000,460000}" {
		t.Fatal("remapped output", text, err)
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), target.CatalogRead(), old); err == nil {
		t.Fatal("foreign stored expression admitted")
	}
	if _, err := (scalarRenderer{}).sql(context.Background(), target.CatalogRead(), n); err != nil {
		t.Fatal("remapped stored expression", err)
	}
}
