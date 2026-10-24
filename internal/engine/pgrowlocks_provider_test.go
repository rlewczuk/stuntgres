package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgrowlocksUnitInstalled(t *testing.T, e *Engine) *catalog.Extension {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "pgrowlocks", ""))
	return e.CatalogRead().MustExtension(e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgrowlocks"}))
}
func pgrowlocksUnitExpression(t *testing.T, e *Engine) plan.InstalledPgrowlocksExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pgrowlocks"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "typed"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.InstalledPgrowlocksExpr)
	return n
}
func TestPgrowlocksProviderIdentity(t *testing.T) {
	e := New()
	ext := pgrowlocksUnitInstalled(t, e)
	view := e.CatalogRead()
	p := view.MustProcedure(ext.Members[0])
	control := extension.StockPgrowlocks.Control()
	if control.Version != "1.2" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "show row-level locking information" {
		t.Fatal(control)
	}
	if err := extension.StockPgrowlocks.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	if p.Name() != "pgrowlocks" || p.Signature != "25" || p.Result != 2249 || p.Volatility != expr.Volatile || !p.Strict || !procedureReturnsSet(p) || p.Owner != ext.Owner || p.Namespace() != ext.TargetNamespace {
		t.Fatal("release declaration", p)
	}
	n := pgrowlocksUnitExpression(t, e)
	if err := stored.ValidateInstalledPgrowlocks(n, view); err != nil {
		t.Fatal(err)
	}
	names := [6]string{"locked_row", "locker", "multi", "xids", "modes", "pids"}
	types := [6]sqltype.ID{27, 28, 16, 1011, 1009, 1007}
	for i, name := range names {
		m := n.ResultUsage().Type.CompositeMember(i)
		owned, _ := view.LookupType(types[i])
		if m.Name != name || m.Usage.Type != owned {
			t.Fatal("actual OUT descriptor", i, m)
		}
	}
	for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Signature = "23" }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Volatility = expr.Stable }, func(p *catalog.Procedure) { p.ObjectRevision = 0 }, func(p *catalog.Procedure) { p.Implementation = new(extension.PgrowlocksProvider) }, func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} }, func(p *catalog.Procedure) { p.ObjectNamespace = ids.ObjectAddress{} }} {
		state := catalog.CloneState(view.State())
		bad := *p
		mutate(&bad)
		state.SetDescriptor(p.Address(), &bad)
		if err := extension.StockPgrowlocks.ValidateExtension(e.catalog.Read(state), ext); err == nil {
			t.Fatal("forged declaration accepted", bad)
		}
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	memberOwner := roleAttributeCreate(t, e, op, "pgrowlocks_member_owner", true, parser.RoleOptions{})
	ownedState := catalog.CloneState(view.State())
	ownedProcedure := *p
	ownedProcedure.Owner = memberOwner.Ref
	ownedState.SetDescriptor(p.Address(), &ownedProcedure)
	metadata, err := extension.OwnerMetadata(e.catalog.Read(ownedState), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	// Restore decodes lexer-provided comment bodies, not complete SQL lines.
	var records []extension.OwnerRecord
	_, err = parser.ParseWithOptions("CREATE EXTENSION pgrowlocks;\n"+metadata, parser.Options{
		Context: context.Background(), StandardConformingStrings: true,
		LineComment: func(comment string, _ parser.Position) error {
			record, found, err := extension.DecodeOwners(comment)
			if err == nil && found {
				records = append(records, record)
			}
			return err
		},
	})
	if err != nil || len(records) != 1 {
		t.Fatal("dump owner metadata", records, err)
	}
	record := records[0]
	expectedMember := extension.MemberOwner{Slot: 0, Schema: "public", Kind: "procedure", Name: "pgrowlocks", Signature: "25", Owner: memberOwner.Name}
	if record.Name != "pgrowlocks" || record.Schema != "public" || record.Version != "1.2" || record.Owner != "postgres" || len(record.Members) != 1 || record.Members[0] != expectedMember {
		t.Fatal("dump owner metadata", record)
	}
	replay := e.restoreStage()
	replayOp, replayDone := roleOperation(t, replay, "postgres")
	defer replayDone()
	if err := extensionUnitExecute(t, replay, replayOp, &parser.CreateExtension{Name: parser.Identifier{Text: "pgrowlocks"}}); err != nil {
		t.Fatal("replay installation", err)
	}
	replayedExtension := replay.CatalogRead().MustExtension(replay.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgrowlocks"}))
	if replay.CatalogRead().MustProcedure(replayedExtension.Members[0]).Owner != replayedExtension.Owner {
		t.Fatal("fresh installation did not use installation owner")
	}
	if err := replay.importExtensionOwners(context.Background(), replayOp, records); err != nil {
		t.Fatal("owner replay", err)
	}
	replayedView := replay.CatalogRead()
	replayedOwner, ok := replay.Roles().Lookup(memberOwner.Name)
	if !ok || replayedView.MustProcedure(replayedExtension.Members[0]).Owner != replayedOwner.Ref || replayedView.MustExtension(replayedExtension.Address()).Owner != replayedExtension.Owner || p.Owner != ext.Owner {
		t.Fatal("owner replay changed installation owner or lost member owner")
	}
	if err := extension.StockPgrowlocks.ValidateExtension(replayedView, replayedView.MustExtension(replayedExtension.Address())); err != nil {
		t.Fatal("owner replay invalidated provider", err)
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}, p.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 900001}}
	state := catalog.CloneState(view.State())
	for old := range mapping {
		state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
		state.RemoveDescriptor(old)
	}
	for old, destination := range mapping {
		d := extension.RemapDescriptor(view.Get(old), mapping)
		state.SetDescriptor(destination, d)
		state.BindName(catalog.DescriptorNameKey(d), destination)
	}
	state.SetEpoch(state.Epoch() + 1)
	destination := e.catalog.Read(state)
	remap := func(ref ids.ObjectRef) ids.ObjectRef {
		ref.Epoch = state.Epoch()
		if a, ok := mapping[ref.Address]; ok {
			ref.Address = a
		}
		return ref
	}
	mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(mapping, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "locks", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	remapped, err := mapper.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	mapped := remapped.(*plan.ProjectNode).Expressions[0]
	m := mapped.(plan.InstalledPgrowlocksExpr)
	if m.Ref != remap(n.Ref) || !exec.SameRoutineDeclaration(destination.MustProcedure(m.Ref.Address), m.Procedure) || m.Procedure.Revision() != n.Procedure.Revision() || n.Procedure != view.Get(n.Ref.Address) {
		t.Fatal("remapped installed authority", m)
	}
	text, err := (stored.Deparser{View: destination}).SQL(context.Background(), m, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "pgrowlocks") {
		t.Fatal(text, err)
	}
	state.RemoveDescriptor(m.Ref.Address)
	if err := stored.ValidateInstalledPgrowlocks(m, destination); err == nil {
		t.Fatal("removed provider admitted")
	}
}
