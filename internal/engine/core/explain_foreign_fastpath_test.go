package core_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
)

type explainCopyProbe struct {fdwUnitSingle; callbacks int}
func (p *explainCopyProbe) ExplainForeignModify(_ context.Context,r catalog.ForeignExplainModifyRequest)([]catalog.ForeignExplainProperty,error){
	p.callbacks++
	r.Columns[0].Name="callback-only"
	r.RequiredAttributes[0]=99
	r.Parameters.Values[0]=int64(99)
	return nil,nil
}

func TestExplainAnalyzeForeignObservationFastPath(t *testing.T){
	provider:=&explainCopyProbe{fdwUnitSingle:fdwUnitSingle{fdwUnitInsert:&fdwUnitInsert{present:true}}}
	source:=&exec.ForeignQuerySource{Wrapper:provider,Request:catalog.ForeignRequest{Columns:[]catalog.ForeignColumn{{Name:"k",Attribute:1}},RequiredAttributes:[]int32{1},Parameters:catalog.ForeignScanParameters{Values:[]driver.Value{int64(1)}}}}
	ctx:=context.Background()
	var sink *fdw.Modification
	// This is the same required planning allocation/copy, excluding only the
	// optional observation seam. Compare bounded planning, not whole-engine cost.
	baseline:=testing.AllocsPerRun(20,func(){
		if err:=fdw.OperationSupported(provider,catalog.ForeignInsert,source.Request.Table);err!=nil {t.Fatal(err)}
		sink=&fdw.Modification{Source:source,Request:catalog.ForeignModifyRequest{ForeignRequest:exec.CopyForeignRequest(source.Request),Operation:catalog.ForeignInsert}}
	})
	without:=testing.AllocsPerRun(20,func(){var err error;sink,err=fdw.PlanModification(ctx,source,catalog.ForeignInsert,false);if err!=nil {t.Fatal(err)}})
	if sink==nil || provider.callbacks!=0 || without>baseline {t.Fatal("nil observation added request-copy work",without,baseline,provider.callbacks)}
	observed,_:=exec.Observe(ctx,false)
	modification,err:=fdw.PlanModification(observed,source,catalog.ForeignInsert,false)
	if err!=nil || provider.callbacks!=1 {t.Fatal(err,provider.callbacks)}
	if modification.Request.Columns[0].Name!="k" || modification.Request.RequiredAttributes[0]!=1 || modification.Request.Parameters.Values[0]!=int64(1) || source.Request.Columns[0].Name!="k" {t.Fatal("callback borrowed planning request",modification.Request)}
}
