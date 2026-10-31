package core_test

import (
	"context"
	"testing"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestPgstattupleHeapExpressionIdentity(t *testing.T) {
	e:=engine.New()
	ext:=pgstatindexUnitInstalled(t,e)
	view:=e.CatalogRead()
	// The eight-member inventory is independently asserted in public SQL; this
	// supplement proves genuine sealed heap declarations and retained expression roots.
	for _,address:=range ext.Members[3:] {
		p:=view.MustProcedure(address)
		if err:=extension.StockPgstattuple.ValidateProcedure(view,p); err!=nil {t.Fatal(err)}
		for _,mutate:=range []func(*catalog.Procedure){
			func(p *catalog.Procedure){p.Strict=false},
			func(p *catalog.Procedure){p.Result=25},
			func(p *catalog.Procedure){p.Installed.Parallel='r'},
			func(p *catalog.Procedure){p.Display.Source="pgstatindex_v1_5"},
		} {
			state:=catalog.CloneState(view.State())
			bad:=*p
			mutate(&bad)
			state.SetDescriptor(address,&bad)
			if extension.StockPgstattuple.ValidateProcedure(e.Catalog.Read(state),&bad)==nil {t.Fatal("forged installed member accepted")}
		}
	}
	for _,name:=range []string{"pgstattuple","pgstattuple_approx","pg_relpages"} {
		n:=pgstattupleHeapUnitExpression(t,e,name,"-")
		if n.Properties().Volatility!=expr.Volatile || !n.Properties().StageDependent || n.CallRoutineReference()!=n.Procedure {t.Fatal(n)}
		rebuilt,err:=n.Rebuild(expr.InfoOf(n),n.Children())
		if err!=nil {t.Fatal(err)}
		copy:=rebuilt.(plan.InstalledPgstattupleExpr)
		if copy.Ref!=n.Ref || copy.Procedure!=n.Procedure || copy.Kind!=n.Kind || copy.Input!=n.Input || &copy.Args[0]==&n.Args[0] {t.Fatal("rebuild lost identity or borrowed mutable operand vector")}
		if err:=stored.ValidateInstalledPgstattuple(copy,view); err!=nil {t.Fatal(err)}
		for _,mutate:=range []func(*plan.InstalledPgstattupleExpr){
			func(n *plan.InstalledPgstattupleExpr){n.RoutineReference=nil},
			func(n *plan.InstalledPgstattupleExpr){n.Ref.Address.ObjectID++},
			func(n *plan.InstalledPgstattupleExpr){n.Input=sqltype.Int4},
			func(n *plan.InstalledPgstattupleExpr){n.Kind=functions.HeapStatisticsKind(255)},
		} {
			bad:=n
			mutate(&bad)
			if stored.ValidateInstalledPgstattuple(bad,view)==nil {t.Fatal("forged call authority accepted")}
		}
		text,err:=(stored.Deparser{View:view}).SQL(context.Background(),n,stored.DeparseParent{})
		if err!=nil || text=="" {t.Fatal(text,err)}
		mapping:=map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():{ClassID:ids.ExtensionClassID,ObjectID:910000}}
		for i,address:=range ext.Members {mapping[address]=ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:ids.OID(910001+i)}}
		state:=catalog.CloneState(view.State())
		descriptor:=extension.DescriptorRemapper(mapping)
		for old:=range mapping {state.UnbindName(catalog.DescriptorNameKey(view.Get(old)));state.RemoveDescriptor(old)}
		for old,destination:=range mapping {d:=descriptor(view.Get(old));state.SetDescriptor(destination,d);state.BindName(catalog.DescriptorNameKey(d),destination)}
		state.SetEpoch(state.Epoch()+1)
		remap:=func(ref ids.ObjectRef) ids.ObjectRef {ref.Epoch=state.Epoch();if address,ok:=mapping[ref.Address];ok {ref.Address=address};return ref}
		mapper:=stored.ViewQueryRemapper{Catalog:e.Catalog.Read(state),Ref:remap,Type:func(d *sqltype.Descriptor)*sqltype.Descriptor{return d},Procedure:extension.ProcedureRemapper(descriptor,nil,nil)}
		query:=&plan.ProjectNode{QuerySchema:plan.QuerySchema{{Name:"s",Usage:n.ResultUsage()}},Child:&plan.ValuesNode{Rows:[][]expr.Expr{{}}},Expressions:[]expr.Expr{n}}
		mappedQuery,err:=mapper.Query(query)
		if err!=nil {t.Fatal(err)}
		mapped:=mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgstattupleExpr)
		if mapped.Ref!=remap(n.Ref) || mapped.Procedure.Address()!=mapping[n.Procedure.Address()] || mapped.CallRoutineReference()!=mapped.Procedure || n.Procedure!=view.Get(n.Ref.Address) {t.Fatal("replacement remap lost selected immutable provider identity")}
		if err:=stored.ValidateInstalledPgstattuple(mapped,e.Catalog.Read(state));err!=nil {t.Fatal(err)}
	}
}

func TestPgstattupleHeapDefaultClosure(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	for _, name := range []string{"pgstattuple", "pgstattuple_approx", "pg_relpages"} {
		n := pgstattupleHeapUnitExpression(t, e, name, "-")
		if !analyze.ClosedPgstattupleDefault(n, view) {
			t.Fatal("authenticated closed installed call rejected", name)
		}
		// The default retains the original volatile/stage-sensitive call,
		// not a computed value or a lowered-volatility substitute.
		if n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("default closure changed invocation properties", name)
		}
		if name == "pg_relpages" {
			if text, err := stored.DefaultSQL(context.Background(), view, n); err != nil || text == "" {
				t.Fatal("authenticated stored scalar default rejected", text, err)
			}
		}
		rejectStored := func(invalid expr.Expr) {
			t.Helper()
			if _, err := stored.DefaultSQL(context.Background(), view, invalid); err == nil {
				t.Fatal("stored default renderer bypassed bounded provider closure", name)
			}
		}
		bad := n
		bad.RoutineReference = nil
		if analyze.ClosedPgstattupleDefault(bad, view) {
			t.Fatal("default closure accepted unauthenticated provider", name)
		}
		rejectStored(bad)
		if analyze.ClosedPgstattupleCheck(bad, view) {
			t.Fatal("CHECK closure accepted unauthenticated provider", name)
		}
		bad = n
		bad.Ref.Epoch++
		if analyze.ClosedPgstattupleDefault(bad, view) {
			t.Fatal("default closure accepted stale provider", name)
		}
		rejectStored(bad)
		if analyze.ClosedPgstattupleCheck(bad, view) {
			t.Fatal("CHECK closure accepted stale provider", name)
		}
		parameter := plan.ParameterExpr{Info: expr.InfoOf(n.Args[0]), Number: 1}
		parameter.Property.ParameterDependent = true
		bad = n
		bad.Args = []expr.Expr{parameter}
		if analyze.ClosedPgstattupleDefault(bad, view) {
			t.Fatal("default closure admitted execution-frame argument", name)
		}
		rejectStored(bad)
		column := plan.ColumnExpr{Info: expr.InfoOf(n.Args[0])}
		column.Property.RowDependent = true
		bad.Args = []expr.Expr{column}
		if analyze.ClosedPgstattupleDefault(bad, view) {
			t.Fatal("default closure admitted row argument", name)
		}
		rejectStored(bad)
		if !analyze.ClosedPgstattupleCheck(bad, view) {
			t.Fatal("owner-bound CHECK rejected an ordinary row argument", name)
		}
		stage := plan.LiteralExpr{Info: expr.InfoOf(n.Args[0]), Value: sqltype.Null(n.Args[0].ResultUsage().Type)}
		stage.Property.StageDependent = true
		unrelated := n
		unrelated.Args = []expr.Expr{stage}
		if analyze.ClosedPgstattupleDefault(unrelated, view) || analyze.ClosedPgstattupleCheck(unrelated, view) {
			t.Fatal("provider closure admitted an unrelated stage producer", name)
		}
		rejectStored(unrelated)
	}
}

func TestPgstattupleHeapRetainedRecipeClosure(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	view := e.CatalogRead()
	for _, name := range []string{"pgstattuple", "pgstattuple_approx", "pg_relpages"} {
		n := pgstattupleHeapUnitExpression(t, e, name, "-")
		original := n.Procedure
		state := catalog.CloneState(view.State())
		current := *original
		current.ObjectName = "retained_" + name
		state.UnbindName(catalog.DescriptorNameKey(original))
		state.SetDescriptor(current.Address(), &current)
		state.BindName(catalog.DescriptorNameKey(&current), current.Address())
		moved := e.Catalog.Read(state)
		if &current == original || !analyze.ClosedPgstattupleDefault(n, moved) || !analyze.ClosedPgstattupleCheck(n, moved) {
			t.Fatal("retained executable declaration rejected after presentation change", name)
		}
		// The recursive recipe boundary must authenticate the retained leaf too;
		// this is not just a binder admission or a direct function deparse.
		boolean, _ := moved.LookupType(sqltype.Bool)
		wrapped := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage(), Property: n.Properties()}, Child: n}
		text, err := stored.DefaultSQL(context.Background(), moved, wrapped)
		if err != nil || text == "" {
			t.Fatal("retained wrapped recipe rejected", name, text, err)
		}
		if n.Procedure != original || n.CallRoutineReference() != original || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("recipe admission replaced captured authority or properties", name)
		}
		reject := func(bad plan.InstalledPgstattupleExpr, target catalog.Read) {
			t.Helper()
			if analyze.ClosedPgstattupleDefault(bad, target) || analyze.ClosedPgstattupleCheck(bad, target) {
				t.Fatal("retained recipe closure accepted invalid authority", name)
			}
			wrapped.Child = bad
			if _, err := stored.DefaultSQL(context.Background(), target, wrapped); err == nil {
				t.Fatal("recursive rendering accepted invalid retained authority", name)
			}
		}
		bad := n
		bad.Ref.Epoch++
		reject(bad, moved)
		bad = n
		bad.RoutineReference = &current
		reject(bad, moved)
		bad = n
		bad.Input = sqltype.Int4
		reject(bad, moved)
		damaged := current
		damaged.Strict = false
		state.SetDescriptor(damaged.Address(), &damaged)
		reject(n, e.Catalog.Read(state))
	}
}

func TestPgstattupleHeapScalarNullSource(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	for _, name := range []string{"pgstattuple", "pgstattuple_approx", "pg_relpages"} {
		n := pgstattupleHeapUnitExpression(t, e, name, "-")
		want := name == "pg_relpages"
		out := name
		if want {
			out = ""
		}
		if expr.SourceOutName(n) != out {
			t.Fatal("source metadata invented a scalar OUT declaration", name)
		}
		if exec.StrictScalarFunctionSource(n) != want {
			t.Fatal("scalar source classification lost bigint/RECORD distinction", name)
		}
		info := expr.InfoOf(n.Args[0])
		n.Args = []expr.Expr{plan.LiteralExpr{Info: info, Value: sqltype.Null(info.Usage.Type)}}
		folded, changed, err := exec.FoldStrictParameterNull(&exec.Context{}, n)
		if err != nil || changed != want {
			t.Fatal("strict NULL folding lost bigint/RECORD distinction", name, changed, err)
		}
		if want && (!exec.RandomLiteralNull(folded) || folded.ResultUsage() != n.ResultUsage()) {
			t.Fatal("folded scalar source lost typed NULL", name)
		}
	}
}
