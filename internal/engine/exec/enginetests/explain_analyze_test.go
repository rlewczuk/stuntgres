package enginetests_test

import (
	"context"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestExplainAnalyzeInstrumentation(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(integer, 1)
	schema := plan.QuerySchema{{Name: "v", Usage: integer.Usage()}}
	makeValues := func() *plan.ValuesNode {
		literal := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: one}
		return &plan.ValuesNode{QuerySchema: schema, Rows: [][]expr.Expr{{literal},{literal},{literal}}}
	}
	for _, timing := range []bool{false,true} {
		t.Run(map[bool]string{false:"counts",true:"timing"}[timing],func(t *testing.T) {
			ctx, observed := exec.Observe(context.Background(),timing)
			values:=makeValues()
			observed.Register(values)
			stage,err:=exec.OpenNode(ctx,&exec.State{Source:&exec.Source{Singleton:true}},values)
			if err!=nil {t.Fatal(err)}
			if actual:=observed.Actual(values);actual.Loops!=0 || actual.Rows!=0 {t.Fatal("opening is not demand",actual)}
			if _,present,err:=stage.NextOccurrence(ctx);err!=nil || !present {t.Fatal(present,err)}
			if actual:=observed.Actual(values);actual.Rows!=1 || actual.Loops!=1 {t.Fatal("capacity counted as output",actual)}
			// Expression access for an already pulled ordinal is not another row.
			if present,err:=stage.ProjectOrdinal(ctx,0,nil);err!=nil || !present {t.Fatal(present,err)}
			if actual:=observed.Actual(values);actual.Rows!=1 || actual.Loops!=1 {t.Fatal("projection double counted",actual)}
			if _,err:=stage.Rewind(ctx);err!=nil {t.Fatal(err)}
			if actual:=observed.Actual(values);actual.Loops!=1 {t.Fatal("rewind without demand executed",actual)}
			if err:=stage.Drain(ctx);err!=nil {t.Fatal(err)}
			if err:=stage.Close();err!=nil {t.Fatal(err)}
			actual:=observed.Actual(values)
			if actual.Rows!=4 || actual.Loops!=2 || actual.Elapsed<0 {t.Fatal("invocation aggregation",actual)}
			if !timing && actual.Elapsed!=0 {t.Fatal("TIMING OFF sampled clock",actual)}
		})
	}
	t.Run("zero-and-never-demanded",func(t *testing.T) {
		ctx,observed:=exec.Observe(context.Background(),false)
		empty:=&plan.ValuesNode{QuerySchema:schema}
		unused:=makeValues()
		observed.Register(unused)
		stage,err:=exec.OpenNode(ctx,&exec.State{Source:&exec.Source{Singleton:true}},empty)
		if err!=nil {t.Fatal(err)}
		if err:=stage.Drain(ctx);err!=nil {t.Fatal(err)}
		stage.Close()
		if actual:=observed.Actual(empty);actual.Rows!=0 || actual.Loops!=1 {t.Fatal(actual)}
		if actual:=observed.Actual(unused);actual.Rows!=0 || actual.Loops!=0 {t.Fatal(actual)}
	})
	t.Run("joined-worker-local-records",func(t *testing.T) {
		ctx,observed:=exec.Observe(context.Background(),true)
		values:=makeValues()
		var group sync.WaitGroup
		errors:=make(chan error,4)
		for range 4 {
			group.Add(1)
			go func(){
				defer group.Done()
				stage,err:=exec.OpenNode(ctx,&exec.State{Source:&exec.Source{Singleton:true}},values)
				if err==nil {err=stage.Drain(ctx)}
				if closeErr:=stage.Close();err==nil {err=closeErr}
				errors<-err
			}()
		}
		group.Wait()
		close(errors)
		for err:=range errors {if err!=nil {t.Fatal(err)}}
		if actual:=observed.Actual(values);actual.Rows!=12 || actual.Loops!=4 || actual.Elapsed<0 {t.Fatal(actual)}
	})
}
