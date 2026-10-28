package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRuntimeInfoBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct { call string; oid uint32; strict, set bool; volatility expr.Volatility }{
		{"pg_jit_available()",315,true,false,expr.Volatile},
		{"pg_numa_available()",4099,true,false,expr.Stable},
		{"pg_get_loaded_modules()",6353,true,true,expr.Volatile},
		{"pg_current_logfile()",3800,false,false,expr.Volatile},
		{"pg_current_logfile(NULL::text)",3801,false,false,expr.Volatile},
	} {
		node, _, _ := strictNullUnitBind(t,e,op,scope,tc.call)
		p, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || p == nil || uint32(p.Address().ObjectID) != tc.oid || p.Strict != tc.strict || p.ReturnsSet() != tc.set || p.Volatility != tc.volatility { t.Fatal("released declaration", tc, p, err) }
		if tc.set {
			n := node.(functions.LoadedModules)
			if n.Validate() != nil || p.RowEstimate() != 10 || n.Usage.Type.ID() != 2249 { t.Fatal("fixed record declaration", n, p) }
			out := p.OutputDeclaration()
			for i, name := range []string{"module_name","version","file_name"} {
				m := n.Usage.Type.CompositeMember(i)
				if m.Name != name || m.Attribute != int32(i+1) || m.Usage.Type.ID() != sqltype.Text || out.Name(i) != name || out.Type(i) != 25 { t.Fatal("fixed OUT shape", m, out) }
			}
			bad := n; bad.Usage.Type = nil
			if bad.Validate() == nil { t.Fatal("nil record accepted") }
			members := []sqltype.CompositeMember{n.Usage.Type.CompositeMember(0), n.Usage.Type.CompositeMember(1), n.Usage.Type.CompositeMember(2)}
			for _, defect := range []string{"count","name","attribute","type"} {
				changed := append([]sqltype.CompositeMember(nil),members...)
				switch defect { case "count": changed = changed[:2]; case "name": changed[1].Name="wrong"; case "attribute": changed[1].Attribute=4; case "type": changed[1].Usage=node.ResultUsage() }
				shape, err := sqltype.RecordShape(changed); if err != nil { t.Fatal(err) }; bad.Usage=shape.Usage()
				if bad.Validate()==nil { t.Fatal("invalid shape accepted",defect) }
			}
		}
	}
}

func TestRuntimeInfoProtocol(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, call := range []string{"pg_jit_available()","pg_numa_available()","pg_get_loaded_modules()","pg_current_logfile()","pg_current_logfile('stderr')"} {
		node, _, _ := strictNullUnitBind(t,e,op,scope,call)
		retagged, changed, err := stored.RetagTypes(e.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed || !reflect.DeepEqual(retagged, node) { t.Fatal("identity retag changed immutable recipe", retagged, changed, err) }
		switch n := node.(type) {
		case functions.RuntimeCapability:
			if n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || n.ValidateStoredProvider()!=nil { t.Fatal(n) }
			info:=n.Info;info.Occurrence++
			next,err:=n.Rebuild(info,nil);if err!=nil || next.(functions.RuntimeCapability).Info!=info || next.(functions.RuntimeCapability).NUMA!=n.NUMA { t.Fatal(next,err) }
			if _,err:=n.Rebuild(info,[]expr.Expr{n});err==nil { t.Fatal("leaf children accepted") }
			r,err:=n.Render();if err!=nil || r.Name!=call[:len(call)-2] { t.Fatal(r,err) }
			capture,ok,err:=n.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Count!=0 { t.Fatal(capture,ok,err) }
		case functions.LoadedModules:
			info:=n.Info;info.Occurrence++
			next,err:=n.Rebuild(info,nil);if err!=nil || next.(functions.LoadedModules).Info!=info || next.ResultUsage().Type!=n.Usage.Type { t.Fatal(next,err) }
			if n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || n.ValidateStoredProvider()!=nil { t.Fatal(n) }
			r,err:=n.Render();if err!=nil||r.Name!="pg_get_loaded_modules" { t.Fatal(r,err) }
		case functions.CurrentLogfile:
			before:=n;info:=n.Info;info.Occurrence++
			next,err:=n.Rebuild(info,n.Children());if err!=nil { t.Fatal(err) }; rebuilt:=next.(functions.CurrentLogfile)
			if rebuilt.Info!=info || rebuilt.Format!=n.Format || rebuilt.RoutineReference!=n.RoutineReference || !reflect.DeepEqual(n,before) { t.Fatal("retained child/provenance changed",next) }
			if n.PlanningFold()!=expr.FoldNever||n.PreflightFold()!=expr.PreflightKeep||n.ValidateStoredProvider()!=nil { t.Fatal(n) }
			r,err:=n.Render();if err!=nil||r.Name!="pg_current_logfile"||len(r.Inputs)!=len(n.Children()) { t.Fatal(r,err) }
			capture,ok,err:=n.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Count!=len(n.Children()) { t.Fatal(capture,ok,err) }
			if n.Format!=nil&&capture.Inline[0]!=n.Format { t.Fatal("capture lost exact operand") }
			if _,err:=n.Rebuild(info,append(n.Children(),n));err==nil { t.Fatal("wrong arity accepted") }
		}
	}
}

func TestRuntimeInfoEvaluation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, call := range []string{"pg_jit_available()","pg_numa_available()","pg_get_loaded_modules()","pg_current_logfile()","pg_current_logfile(NULL::text)","pg_current_logfile('stderr')","pg_current_logfile('csvlog')","pg_current_logfile('jsonlog')"} {
		node, _, evaluation := strictNullUnitBind(t,e,op,scope,call)
		var cursor expr.SourceCursor;var err error
		switch n:=node.(type) {
		case functions.LoadedModules:
			if _,err:=n.Eval(&evaluation);err==nil { t.Fatal("set accepted scalar evaluation") }
			cursor,err=n.OpenSource(&evaluation)
		case functions.RuntimeCapability:
			v,err:=n.Eval(&evaluation);if err!=nil { t.Fatal(err) };flag,err:=v.Boolean();if err!=nil||flag||v.IsNull() { t.Fatal(v,err) }
			cursor,err=n.OpenSource(&evaluation)
		case functions.CurrentLogfile:
			v,err:=n.Eval(&evaluation);if err!=nil||!v.IsNull()||v.Type()!=n.Usage.Type { t.Fatal(v,err) }
			cursor,err=n.OpenSource(&evaluation)
		}
		if err!=nil||cursor.Type!=node.ResultUsage().Type { t.Fatal(cursor,err) }
		_,present,err:=cursor.Next(context.Background());_,set:=node.(functions.LoadedModules)
		if err!=nil||present==set { t.Fatal("scalar/empty distinction",present,err) };cursor.Close()
		ctx,cancel:=context.WithCancel(context.Background());cancel();evaluation.Context=ctx
		switch n:=node.(type) { case functions.RuntimeCapability: _,err=n.Eval(&evaluation);case functions.CurrentLogfile: _,err=n.Eval(&evaluation);case functions.LoadedModules: _,err=n.OpenSource(&evaluation) }
		if !errors.Is(err,context.Canceled) { t.Fatal("cancellation suppressed",err) }
	}
	for _, value:=range []string{"syslog","STDERR",""," stderr","a\"b\\c\nλ"} {
		node,_,evaluation:=strictNullUnitBind(t,e,op,scope,"pg_current_logfile('"+strings.ReplaceAll(value,"'","''")+"')")
		n:=node.(functions.CurrentLogfile)
		_,err:=n.Eval(&evaluation);var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic)||diagnostic.Code!="22023"||diagnostic.Message!="log format \""+value+"\" is not supported"||diagnostic.Hint!="The supported log formats are \"stderr\", \"csvlog\", and \"jsonlog\"."||diagnostic.Position!="" { t.Fatal(diagnostic,err) }
	}
}
