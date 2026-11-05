package schemacmds_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIntarraySliceProviderIdentity(t *testing.T) {
	e,ext,_:=intarrayUnitInstalled(t)
	view:=e.CatalogRead()
	if len(ext.Members)!=41 || len(ext.Installation.Members)!=39 || len(ext.Installation.Declarations)!=39 { t.Fatal("closed slice recipe") }
	prefix:=[]string{"query_int","bqarr_in","bqarr_out","intbig_gkey","_intbig_in","_intbig_out","_int_overlap","_int_contains","_int_contained","intarray_push_elem","intarray_push_array","intarray_del_elem","intset_subtract","intset_union_elem","_int_union","_int_inter","_int_overlap_sel","_int_contains_sel","_int_contained_sel","_int_overlap_joinsel","_int_contains_joinsel","_int_contained_joinsel","&&","@>","<@","+","+","-","-","|","|","&"}
	for slot,name:=range prefix {
		d:=view.Get(ext.Installation.Members[slot])
		class:=ids.ProcedureClassID
		if slot==0 || slot==3 { class=ids.TypeClassID } else if slot>=22 { class=ids.OperatorClassID }
		if d.Name()!=name || d.Address().ClassID!=class { t.Fatal("preserved original slot changed",slot,d) }
	}
	for i,tc:=range []struct{name string; signature catalog.ProcedureSignature; result ids.OID}{
		{"icount","1007",23},{"idx","1007,23",23},{"subarray","1007,23",1007},{"subarray","1007,23,23",1007},{"intset","23",1007},
	} {
		slot:=32+i
		proc:=view.MustProcedure(ext.Installation.Members[slot])
		operation,ok:=extension.IntarraySliceProcedure(view,proc)
		if !ok || operation!=sqltype.IntarraySliceOperation(i) || proc.Name()!=tc.name || proc.Signature!=tc.signature || proc.Result!=tc.result || !proc.Strict || proc.Volatility!=expr.Immutable || proc.Display.Source!=tc.name || proc.Display.Binary!="$libdir/_int" || proc.Origin.Installation!=ext.Installation || proc.Origin.Declaration!=slot { t.Fatal("exact slice declaration",slot,proc) }
		if _,ok:=extension.IntarraySetProcedure(view,proc); ok { t.Fatal("slice admitted as SET role") }
		for _,mutate:=range []func(*catalog.Procedure){
			func(p *catalog.Procedure){p.Signature="1007,25"},func(p *catalog.Procedure){p.Result=25},
			func(p *catalog.Procedure){p.Strict=false},func(p *catalog.Procedure){p.Volatility=expr.Volatile},
			func(p *catalog.Procedure){p.Display.Source="bqarr_in"},func(p *catalog.Procedure){p.Implementation=extension.StockSeg},
			func(p *catalog.Procedure){o:=*p.Origin;o.Declaration=37;p.Origin=&o},
			func(p *catalog.Procedure){o:=*p.Origin;copied:=*o.Installation;o.Installation=&copied;p.Origin=&o},
			func(p *catalog.Procedure){p.Owner=new(authorization.Domain).Principal(p.Owner.ID())},
		} {
			bad:=*proc;mutate(&bad)
			state:=catalog.CloneState(view.State());state.SetDescriptor(bad.Address(),&bad)
			current:=e.Catalog.Read(state)
			if _,err:=extension.ProcedureSymbol(current,&bad);err==nil { t.Fatal("forged slice declaration admitted",slot) }
			if _,ok:=extension.IntarraySliceProcedure(current,&bad);ok { t.Fatal("forged role executable",slot) }
			if err:=extension.StockIntarray.ValidateProcedure(view,proc);err!=nil { t.Fatal("negative candidate changed source",err) }
		}
	}
	members:=slices.Clone(ext.Members)
	for i:=range 2 {
		address:=ext.Installation.Members[37+i]
		op:=view.MustOperator(address)
		left,right:=ids.OID(0),ids.OID(1007);if i==1 {left,right=1007,23}
		if op.Name()!="#" || op.Function!=ext.Installation.Members[32+i].ObjectID || op.Left!=left || op.Right!=right || op.Result!=23 {t.Fatal("operator/backer shape",i)}
		for _,mutate:=range []func(*catalog.Operator){
			func(o *catalog.Operator){o.Function++},func(o *catalog.Operator){o.Left++},func(o *catalog.Operator){o.Right++},func(o *catalog.Operator){o.Result++},
			func(o *catalog.Operator){o.Commutator++},func(o *catalog.Operator){o.Negator++},func(o *catalog.Operator){o.Restriction++},func(o *catalog.Operator){o.Join++},func(o *catalog.Operator){o.Hash=true},func(o *catalog.Operator){o.Merge=true},
		} {
			bad:=*op;mutate(&bad)
			state:=catalog.CloneState(view.State());state.SetDescriptor(address,&bad)
			if err:=extension.StockIntarray.ValidateExtension(e.Catalog.Read(state),ext);err==nil {t.Fatal("forged operator admitted",i)}
		}
	}
	if !slices.Equal(members,ext.Members) {t.Fatal("validation changed accepted members")}
	t.Run("late_slice_collision",func(t *testing.T){
		target:=engine.New();op,done:=enginetest.RoleOperation(t,target,"postgres");defer done()
		enginetest.RoleExecute(t,target,op,"CREATE FUNCTION subarray(integer[],integer,integer) RETURNS integer[] LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT $1'")
		before:=target.Catalog.State
		target.Mu.Lock()
		stage,err:=schemacmds.StageExtensionLocked(target,&core.StatementScope{Op:op},&parser.CreateExtension{Name:parser.Identifier{Text:"intarray"}})
		target.Mu.Unlock()
		extensionUnitDiscardFailed(t,target,stage)
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code!="42723" || target.Catalog.State!=before {t.Fatal("late collision published prefix",err)}
	})
}
