package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// The core witness supplies actual worker fanout and original-node output.
// This owner-local companion distinguishes active combine/final spans without
// elapsed thresholds or a production observation-inspection API.
func TestExplainAnalyzeParallelAggregateOwnership(t *testing.T) {
	integer:=queryLoopInteger(t)
	ctx,observation:=Observe(context.Background(),true)
	aggregate:=&plan.AggregateNode{}
	probe:=&explainAggregateWorkProbe{t:t,observation:observation,node:aggregate,integer:integer}
	aggregate.Calls=[]plan.AggregateCall{{Provider:probe}}
	rows,err:=finalizeAggregatePartitions(ctx,aggregate,[][]QueryGroup{{{Keys:make([]sqltype.Value,1),states:[]any{int64(2)}}},{{Keys:make([]sqltype.Value,1),states:[]any{int64(2)}}}},&Context{})
	if err!=nil || len(rows)!=1 || probe.combines!=2 || probe.finals!=1 {t.Fatal(rows,err,probe.combines,probe.finals)}
	if value,_:=rows[0][0].Integer();value!=4 {t.Fatal(value)}
	actual:=observation.Actual(aggregate)
	if actual.Rows!=0 || actual.Loops!=0 || actual.Elapsed<0 || observation.records[aggregate][0].depth!=0 {t.Fatal("combine contribution invented logical output",actual)}
}

type explainAggregateWorkProbe struct {
	t *testing.T
	observation *Observation
	node plan.Node
	integer *sqltype.Descriptor
	combines,finals int
}
func (p *explainAggregateWorkProbe) active(){
	records:=p.observation.records[p.node]
	if len(records)!=1 || records[0].depth!=1 {p.t.Fatal("support callback outside aggregate active work span",records)}
}
func (p *explainAggregateWorkProbe) ResultUsage() sqltype.Usage{return p.integer.Usage()}
func (p *explainAggregateWorkProbe) Init(context.Context)(any,error){v:=int64(0);return &v,nil}
func (p *explainAggregateWorkProbe) Advance(context.Context,any,[]sqltype.Value,expr.Context)error{p.t.Fatal("leader must combine, not advance");return nil}
func (p *explainAggregateWorkProbe) PartialCapable()bool{return true}
func (p *explainAggregateWorkProbe) Combine(_ context.Context,left,right any,_ expr.Context)error{p.active();p.combines++;*left.(*int64)+=right.(int64);return nil}
func (p *explainAggregateWorkProbe) Finalize(_ context.Context,state any)(sqltype.Value,error){p.active();p.finals++;return sqltype.Integer(p.integer,*state.(*int64))}
