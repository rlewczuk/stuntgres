package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func earthUnitInstalled(t *testing.T) (*Engine, *catalog.Extension, *catalog.Type) {
	t.Helper()
	e, _, _ := cubeUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "earthdistance", ""))
	ext := e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
	return e, ext, e.catalog.state.MustType(ext.Members[0])
}

func TestEarthProviderIdentity(t *testing.T) {
	e, ext, d := earthUnitInstalled(t)
	view := e.CatalogRead()
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view, ext); err != nil {
		t.Fatal(err)
	}
	if ext.Version != "1.2" || len(ext.Members) != 2 || d.SQLType.DomainBase().Type == d.SQLType || !d.SQLType.DomainBase().Type.IsCube() {
		t.Fatal("domain identity")
	}
	for _, a := range ext.Members {
		if err := (stored.TypeProvider{}).Validate(view, view.Get(a)); err != nil {
			t.Fatal(err)
		}
	}
	deps, err := catalog.ExtensionPrerequisites(view, ext)
	if err != nil || len(deps) != 1 || view.Get(deps[0]).Name() != "cube" {
		t.Fatal("actual prerequisite", deps, err)
	}
	control := extension.StockEarthdistance.Control()
	control.Requires[0], *control.Comment = "foreign", "foreign"
	if extension.StockEarthdistance.Control().Requires[0] != "cube" || *extension.StockEarthdistance.Control().Comment == "foreign" {
		t.Fatal("shared control storage")
	}
	for _, mutate := range []func(*plan.EarthCheckExpr){
		func(n *plan.EarthCheckExpr) { n.Property.RowDependent = true },
		func(n *plan.EarthCheckExpr) { n.Value.Property.DomainDependent = false },
		func(n *plan.EarthCheckExpr) { n.Value.Usage = n.Usage },
		func(n *plan.EarthCheckExpr) { n.Kind = 0 },
	} {
		state := *view.State()
		invalid := *d
		invalid.DomainChecks = append([]catalog.DomainCheckConstraint(nil), d.DomainChecks...)
		node := invalid.DomainChecks[0].Expression.(plan.EarthCheckExpr)
		mutate(&node)
		invalid.DomainChecks[0].Expression = node
		state.SetDescriptor(d.Address(), &invalid)
		if err := extension.StockEarthdistance.ValidateExtension(e.catalog.read(&state), ext); err == nil {
			t.Fatal("invalid manifest recipe admitted")
		}
	}
}

func TestEarthProviderConflicts(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.catalog.state
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(&StatementScope{op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "earthdistance"}})
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || stage != nil || e.catalog.state != before {
		t.Fatal("missing prerequisite published", err)
	}
	e, ext, _ := earthUnitInstalled(t)
	op2, done2 := roleOperation(t, e, "postgres")
	defer done2()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op2)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	s := e.BeginCatalogStage(auth.Catalog)
	s.authorizeLocked(auth, op2.Session)
	copy := *ext
	err = extension.StockEarthdistance.Stage(&extensionHost{engine: e, op: op2, auth: auth, stage: s}, &copy, ext.Owner)
	e.mu.Unlock()
	e.DiscardCatalogStage(s)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Context == "" {
		t.Fatal("genuine domain collision", err)
	}
}

func TestEarthDomainExpression(t *testing.T) {
	e, _, d := earthUnitInstalled(t)
	view := e.CatalogRead()
	base := d.SQLType.DomainBase()
	n := d.DomainChecks[0].Expression.(plan.EarthCheckExpr)
	if !n.Valid() {
		t.Fatal("invalid provider expression")
	}
	var refs []ids.ObjectRef
	if _, err := (stored.ScalarRenderer{DomainValue: &base, Dependencies: &refs}).SQL(context.Background(), view, n); err != nil || len(refs) == 0 {
		t.Fatal("real CHECK dependencies", refs, err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), view, n); err == nil {
		t.Fatal("missing VALUE frame admitted")
	}
	for _, invalid := range []plan.EarthCheckExpr{func() plan.EarthCheckExpr { v := n; v.Kind = 0; return v }(), func() plan.EarthCheckExpr { v := n; v.Value.Usage = n.Usage; return v }()} {
		if _, err := (stored.ScalarRenderer{DomainValue: &base}).SQL(context.Background(), view, invalid); err == nil {
			t.Fatal("invalid CHECK recipe admitted")
		}
	}
	foreign, err := sqltype.Cube(base.Type.ID(), "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	bad := n
	bad.Value.Usage = foreign.Usage()
	foreignUsage := foreign.Usage()
	if _, err := (stored.ScalarRenderer{DomainValue: &foreignUsage}).SQL(context.Background(), view, bad); err == nil {
		t.Fatal("foreign generation admitted")
	}
	for _, replacement := range []*sqltype.Descriptor{foreign, nil} {
		_, _, err := stored.RetagTypes(view, n, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
			if typ == base.Type {
				return replacement
			}
			return typ
		})
		if err == nil {
			t.Fatal("foreign/missing remapped cube accepted")
		}
	}
	visited := 0
	plan.WalkExpression(n, func(node expr.Expr) {
		if _, ok := node.(plan.DomainValueExpr); ok {
			visited++
		}
	})
	if visited != 1 {
		t.Fatal("earth CHECK lost lexical VALUE child")
	}
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{DomainValue: sqltype.Null(base.Type), DomainFrameUsage: base, DomainValueActive: true}}
	value, err := n.Eval(&eval)
	if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
		t.Fatal("NULL CHECK truth", value, err)
	}
	eval.DomainValueActive = false
	if _, err := n.Eval(&eval); err == nil {
		t.Fatal("missing runtime VALUE frame admitted")
	}
}

func TestEarthProviderControls(t *testing.T) {
	t.Run("symbolic_companion_owners", earthOwnerCodecUnits)
	source, _, d := earthUnitInstalled(t)
	base := d.SQLType.DomainBase().Type
	v, err := base.Input(context.Background(), "(6378168,NaN)", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err = v.DomainValue(d.SQLType)
	if err != nil {
		t.Fatal(err)
	}
	target := New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	defer closeOwner()
	// Retain a real TEMP object at the source earth type's OID. Do not rely
	// on the number of members allocated by the prerequisite extensions.
	target.mu.Lock()
	target.catalog.nextOID = d.Address().ObjectID
	target.mu.Unlock()
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	if namespace.Address().ObjectID != d.Address().ObjectID {
		t.Fatal("missing explicit retained TEMP collision")
	}
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if target.catalog.state.Get(namespace.Address()) != namespace {
		t.Fatal("retained TEMP namespace was reminted")
	}
	ext := target.catalog.state.MustExtension(target.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
	imported := target.catalog.state.MustType(ext.Members[0])
	if imported.SQLType == d.SQLType || imported.SQLType.ID() == d.SQLType.ID() {
		t.Fatal("missing real identity collision")
	}
	if err := (stored.TypeProvider{}).Validate(target.CatalogRead(), imported); err != nil {
		t.Fatal(err)
	}
	mapped, err := typedef.ReownDomainDatum(v, imported.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{d.SQLType: imported.SQLType, base: imported.SQLType.DomainBase().Type}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := imported.SQLType.Output(context.Background(), mapped); err != nil || text != "(6378168, NaN)" {
		t.Fatal("exact imported payload", text, err)
	}
	for _, check := range imported.DomainChecks {
		n := check.Expression.(plan.EarthCheckExpr)
		if n.Value.Usage != imported.SQLType.DomainBase() {
			t.Fatal("stale child generation")
		}
		for _, dependency := range check.Dependencies {
			if dependency.Ref.Domain() != target.catalog.Domain || dependency.Ref.Epoch != target.CatalogRead().Epoch() {
				t.Fatal("stale dependency identity")
			}
		}
	}
}
