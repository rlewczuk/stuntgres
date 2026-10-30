package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

type explainExecutionToken struct { number int }
type explainSelectedFacts struct {
	base catalog.ForeignRequest
	query *catalog.ForeignQueryRequest
	modify *catalog.ForeignModifyRequest
	explained, begun bool
}

// An executing override of the existing metadata-only probe. Existing plain
// EXPLAIN tests retain their intentionally execution-rejecting methods.
type explainExecutionWrapper struct {
	fdwExplainContextProbe
	facts map[*explainExecutionToken]*explainSelectedFacts
	data map[int64]int64
	declineExecution bool
	ordinaryControl bool // Set only by the separate non-ANALYZE control.
	begins, ends, modifies, modifyBegins, modifyEnds int
	tx fdwUnitTransaction
	check func() error
	failure error
	invalid bool
	cancel context.CancelFunc
	cancelFetch context.CancelFunc
	properties []catalog.ForeignExplainProperty
	observation *exec.Observation
	nodes []plan.Node
}

func (w *explainExecutionWrapper) token(facts *explainSelectedFacts) *explainExecutionToken {
	token := &explainExecutionToken{len(w.facts)+1}; w.facts[token]=facts; return token
}
func (w *explainExecutionWrapper) GetForeignPaths(_ context.Context, r catalog.ForeignRequest, _ catalog.ForeignRelSize) ([]catalog.ForeignPath,error) {
	chosen:=w.token(&explainSelectedFacts{base:exec.CopyForeignRequest(r)})
	expensive:=w.token(&explainSelectedFacts{base:exec.CopyForeignRequest(r)})
	return []catalog.ForeignPath{{TotalCost:99,Attributes:slices.Clone(r.RequiredAttributes),Private:expensive},{TotalCost:1,Attributes:slices.Clone(r.RequiredAttributes),Private:chosen}},nil
}
func (w *explainExecutionWrapper) GetForeignPlan(_ context.Context,r catalog.ForeignRequest,p catalog.ForeignPath)(catalog.ForeignPlan,error) {
	facts, err := w.selected(p.Private)
	if err != nil { return catalog.ForeignPlan{}, err }
	if !reflect.DeepEqual(facts.base,r) || p.TotalCost!=1 {return catalog.ForeignPlan{},fmt.Errorf("wrong executed base selection: request=%+v path=%+v",r,p)}
	return catalog.ForeignPlan{Attributes:slices.Clone(p.Attributes),Private:p.Private},nil
}
func (w *explainExecutionWrapper) queryPaths(r catalog.ForeignQueryRequest)([]catalog.ForeignQueryPath,error) {
	if w.declineExecution {return nil,nil}
	request:=exec.CopyForeignQueryRequest(r)
	token:=w.token(&explainSelectedFacts{query:&request})
	return []catalog.ForeignQueryPath{{TotalCost:1,Compatibility:catalog.ForeignQueryCompatibility{Expressions:true,Collations:true,StatementVisibility:true},Plan:catalog.ForeignQueryPlan{Slots:slices.Clone(r.Stage.Slots),Private:token}}},nil
}
func (w *explainExecutionWrapper) GetForeignJoinPaths(_ context.Context,r catalog.ForeignQueryRequest)([]catalog.ForeignQueryPath,error){return w.queryPaths(r)}
func (w *explainExecutionWrapper) GetForeignUpperPaths(_ context.Context,r catalog.ForeignQueryRequest)([]catalog.ForeignQueryPath,error){return w.queryPaths(r)}
func (w *explainExecutionWrapper) selected(private any) (*explainSelectedFacts, error) {
	token, ok := private.(*explainExecutionToken)
	if !ok || w.facts[token] == nil {return nil,fmt.Errorf("unknown selected token: %T %v",private,private)}
	return w.facts[token],nil
}
func (w *explainExecutionWrapper) explain(ctx context.Context) ([]catalog.ForeignExplainProperty,error) {
	if w.check!=nil {if err:=w.check();err!=nil {return nil,err}}
	if w.cancel!=nil {w.cancel();return nil,ctx.Err()}
	if w.failure!=nil {return nil,w.failure}
	if w.invalid {return []catalog.ForeignExplainProperty{{Name:"invalid:name",Value:"bad"}},nil}
	w.properties=[]catalog.ForeignExplainProperty{{Name:"Execution token",Value:fmt.Sprint(len(w.facts))}}
	return w.properties,nil
}

func explainComparableRequest(r catalog.ForeignRequest) catalog.ForeignRequest {
	r.Transaction, r.Characteristics = nil, nil
	return r
}
func explainComparableQuery(r catalog.ForeignQueryRequest) catalog.ForeignQueryRequest {
	r = exec.CopyForeignQueryRequest(r)
	r.Transaction = nil
	for i := range r.Sources { r.Sources[i].Request = explainComparableRequest(r.Sources[i].Request) }
	return r
}
func (w *explainExecutionWrapper) ExplainForeignScan(ctx context.Context,r catalog.ForeignExplainScanRequest)([]catalog.ForeignExplainProperty,error) {
	if w.ordinaryControl {return nil,errors.New("ordinary control invoked EXPLAIN callback")}
	if r.Query!=nil {
		facts,err:=w.selected(r.Query.Path.Plan.Private)
		if err!=nil {return nil,err}
		if facts.query==nil || facts.explained || !reflect.DeepEqual(*facts.query,r.Query.Request) {return nil,fmt.Errorf("composite rediscovered or changed frame: selected=%+v request=%+v",facts,r.Query.Request)}
		facts.explained=true
		r.Query.Request.Sources[0].Request.Columns[0].Name="callback-owned"
		r.Query.Path.Plan.Slots[0].TypeName="callback-owned"
		if len(r.Query.Request.Parameters.Values)>0 {r.Query.Request.Parameters.Values[0]=int64(-42)}
	} else {
		facts,err:=w.selected(r.Plan.Private)
		if err!=nil {return nil,err}
		if facts.explained || !reflect.DeepEqual(facts.base,explainComparableRequest(r.ForeignRequest)) || r.Path.Private!=r.Plan.Private {return nil,fmt.Errorf("base rediscovered or changed frame: selected=%+v request=%+v",facts.base,r.ForeignRequest)}
		facts.explained=true
		r.Columns[0].Name="callback-owned"
		if len(r.Plan.Attributes)>0 {r.Plan.Attributes[0]=-42}
		if len(r.RequiredAttributes)>0 {r.RequiredAttributes[0]=-42}
		if len(r.Parameters.Values)>0 {r.Parameters.Values[0]=int64(-42)}
	}
	return w.explain(ctx)
}
func (w *explainExecutionWrapper) BeginForeignScan(_ context.Context,r catalog.ForeignRequest,p catalog.ForeignPlan)(catalog.ForeignScan,error) {
	facts,err:=w.selected(p.Private)
	if err!=nil {return nil,err}
	// A modification's internal identity scan is selected independently but
	// explained by the modification, not as an observed query scan.
	if (r.Identity==nil && facts.explained!=!w.ordinaryControl) || facts.begun || !reflect.DeepEqual(facts.base,explainComparableRequest(r)) {
		return nil,fmt.Errorf("base Begin did not use captured plan/frame: explained=%t selected=%+v request=%+v plan=%+v",facts.explained,facts.base,r,p)
	}
	if r.Identity!=nil {
		owned:=false
		for _,selected:=range w.facts {
			if selected.modify==nil || !selected.begun || !selected.explained {continue}
			expected:=explainComparableRequest(selected.modify.ForeignRequest)
			expected.Identity=&selected.modify.Targets
			if reflect.DeepEqual(expected,explainComparableRequest(r)) {owned=true;break}
		}
		if !owned || facts.explained {return nil,errors.New("identity scan lost its explained modification owner")}
	}
	facts.begun=true
	var rows []catalog.ForeignRow;var identities []catalog.ForeignRowIdentity
	for _,k:=range []int64{1,2,3,4} {v,ok:=w.data[k];if !ok {continue};full:=catalog.ForeignRow{k,v};row:=make(catalog.ForeignRow,len(p.Attributes));for i,a:=range p.Attributes {row[i]=full[a-1]};rows=append(rows,row);identities=append(identities,catalog.ForeignRowIdentity{k})}
	w.begins++
	return &explainExecutionScan{fdwDispatchPartition:fdwDispatchPartition{rows:rows},owner:w,identities:identities},nil
}

// This fixture executes the supported source/equality-join/filter/evaluation
// descriptor, rather than returning a canned row count for a SQL string.
func (w *explainExecutionWrapper) queryRows(r catalog.ForeignQueryRequest,s catalog.ForeignQueryStage) ([]catalog.ForeignRow,error) {
	var input []catalog.ForeignRow
	switch s.Kind {
	case "source":
		for _,k:=range []int64{1,2,3,4} {if v,ok:=w.data[k];ok {input=append(input,catalog.ForeignRow{k,v})}}
	case "join":
		left,err:=w.queryRows(r,s.Children[0]);if err!=nil {return nil,err}
		right,err:=w.queryRows(r,s.Children[1]);if err!=nil {return nil,err}
		for _,l:=range left {for _,rr:=range right {input=append(input,append(slices.Clone(l),rr...))}}
	default:
		if len(s.Children)!=1 {return nil,fmt.Errorf("unsupported fixture stage: %s",s.Kind)}
		var err error
		input,err=w.queryRows(r,s.Children[0]);if err!=nil {return nil,err}
	}
	var output []catalog.ForeignRow
	for _,row:=range input {
		if s.Predicate!=nil {
			value,err:=w.queryValue(r,*s.Predicate,row);if err!=nil {return nil,err}
			if !value.(bool) {continue}
		}
		values:=make(catalog.ForeignRow,len(s.Outputs))
		for i,e:=range s.Outputs {value,err:=w.queryValue(r,e,row);if err!=nil {return nil,err};values[i]=value}
		output=append(output,values)
	}
	return output,nil
}
func (w *explainExecutionWrapper) queryValue(r catalog.ForeignQueryRequest,e catalog.ForeignQueryExpression,row catalog.ForeignRow) (driver.Value,error) {
	switch e.Kind {
	case "slot":return row[e.Slot],nil
	case "literal":return e.Literal,nil
	case "parameter":return r.Parameters.Values[e.Parameter-1],nil
	case "comparison":
		left,err:=w.queryValue(r,e.Arguments[0],row);if err!=nil {return nil,err}
		right,err:=w.queryValue(r,e.Arguments[1],row);if err!=nil {return nil,err}
		return left==right,nil
	case "and":
		for _,a:=range e.Arguments {value,err:=w.queryValue(r,a,row);if err!=nil {return nil,err};if !value.(bool) {return false,nil}}
		return true,nil
	case "cast","collate":return w.queryValue(r,e.Arguments[0],row)
	}
	return nil,fmt.Errorf("unsupported fixture expression: %s",e.Kind)
}
func (w *explainExecutionWrapper) BeginForeignQueryScan(_ context.Context,r catalog.ForeignQueryRequest,p catalog.ForeignQueryPlan)(catalog.ForeignScan,error) {
	facts,err:=w.selected(p.Private)
	if err!=nil {return nil,err}
	if facts.query==nil || !facts.explained || facts.begun || !reflect.DeepEqual(*facts.query,explainComparableQuery(r)) || !reflect.DeepEqual(p.Slots,r.Stage.Slots) {return nil,fmt.Errorf("composite Begin diverged from captured token/frame: selected=%+v request=%+v",facts,r)}
	rows,err:=w.queryRows(r,r.Stage)
	if err!=nil {return nil,err}
	facts.begun=true;w.begins++
	return &explainExecutionScan{fdwDispatchPartition:fdwDispatchPartition{rows:rows},owner:w},nil
}

type explainExecutionScan struct {fdwDispatchPartition;owner *explainExecutionWrapper;identities []catalog.ForeignRowIdentity}
func (s *explainExecutionScan) IterateForeignScan(ctx context.Context)(catalog.ForeignRow,bool,error){
	if s.owner.cancelFetch!=nil {s.owner.cancelFetch();return nil,false,ctx.Err()}
	return s.fdwDispatchPartition.IterateForeignScan(ctx)
}
func (s *explainExecutionScan) ReScanForeignScan(ctx context.Context,_ catalog.ForeignScanParameters) error {s.next=0;return ctx.Err()}
func (s *explainExecutionScan) IterateForeignIdentity(ctx context.Context)(catalog.ForeignRow,catalog.ForeignRowIdentity,bool,error){i:=s.next;row,ok,err:=s.IterateForeignScan(ctx);if !ok || err!=nil {return row,nil,ok,err};return row,s.identities[i],true,nil}
func (s *explainExecutionScan) EndForeignScan(ctx context.Context)error {s.owner.ends++;if err:=ctx.Err();err!=nil {return fmt.Errorf("canceled cleanup context: %w",err)};return nil}
func (w *explainExecutionWrapper) ForeignTransactionCapabilities(context.Context,catalog.ForeignRequest)(catalog.ForeignTransactionCapabilities,error){return catalog.ForeignTransactionCapabilities{StatementRollback:true,TransactionRollback:true,Savepoints:true},nil}
func (w *explainExecutionWrapper) BeginForeignTransaction(context.Context,catalog.ForeignRequest)(catalog.ForeignTransaction,error){return &w.tx,nil}
func (w *explainExecutionWrapper) AddForeignUpdateTargets(_ context.Context,_ catalog.ForeignRequest,op catalog.ForeignModifyOperation)(catalog.ForeignUpdateTargets,error){return catalog.ForeignUpdateTargets{Names:[]string{"k"},Private:op},nil}
func (w *explainExecutionWrapper) PlanForeignModify(_ context.Context,r catalog.ForeignRequest,op catalog.ForeignModifyOperation,targets catalog.ForeignUpdateTargets)(catalog.ForeignModifyPlan,error){
	request:=catalog.ForeignModifyRequest{ForeignRequest:exec.CopyForeignRequest(r),Operation:op,Targets:catalog.ForeignUpdateTargets{Names:slices.Clone(targets.Names),Private:targets.Private}}
	token:=w.token(&explainSelectedFacts{modify:&request});return catalog.ForeignModifyPlan{Private:token},nil
}
func (w *explainExecutionWrapper) ExplainForeignModify(ctx context.Context,r catalog.ForeignExplainModifyRequest)([]catalog.ForeignExplainProperty,error){
	facts,err:=w.selected(r.Plan.Private)
	if err!=nil {return nil,err}
	if facts.modify==nil {return nil,errors.New("modification selected a non-modification token")}
	expected:=*facts.modify;expected.Plan=r.Plan
	if facts.explained || !reflect.DeepEqual(expected,r.ForeignModifyRequest) {return nil,fmt.Errorf("modification token/target/frame diverged: selected=%+v request=%+v",expected,r)}
	facts.explained=true
	if len(r.Targets.Names)>0 {r.Targets.Names[0]="callback-owned"}
	r.Columns[0].Name="callback-owned"
	if len(r.Parameters.Values)>0 {r.Parameters.Values[0]=int64(-42)}
	return w.explain(ctx)
}
func (w *explainExecutionWrapper) BeginForeignModify(_ context.Context,r catalog.ForeignModifyRequest)(any,error){
	facts,err:=w.selected(r.Plan.Private)
	if err!=nil {return nil,err}
	if facts.modify==nil {return nil,errors.New("modify Begin selected a non-modification token")}
	expected:=*facts.modify;expected.Plan=r.Plan
	// Transaction enlistment happens after planning; it is a live handle, not
	// part of immutable selected request equality.
	expected.Transaction=r.Transaction
	expected.Characteristics=r.Characteristics
	if !facts.explained || facts.begun || !reflect.DeepEqual(expected,r) {return nil,fmt.Errorf("modify Begin lost selected request: selected=%+v request=%+v",expected,r)}
	facts.begun=true;w.modifyBegins++;return r.Plan.Private,nil
}
func (w *explainExecutionWrapper) EndForeignModify(_ context.Context,r catalog.ForeignModifyRequest,state any)error {if state!=r.Plan.Private {return errors.New("modify state/token lost")};w.modifyEnds++;return nil}
func (w *explainExecutionWrapper) modify(r catalog.ForeignModifyRequest,state any,row catalog.ForeignRow) error {
	facts,err:=w.selected(state)
	if err!=nil {return err}
	if facts.modify==nil {return errors.New("row modification selected a non-modification token")}
	expected:=*facts.modify
	expected.Plan,expected.Transaction,expected.Characteristics=r.Plan,r.Transaction,r.Characteristics
	if state!=r.Plan.Private || !facts.begun || !reflect.DeepEqual(expected,r) {return fmt.Errorf("row modification not owned by captured plan/targets/frame: selected=%+v request=%+v",expected,r)}
	w.modifies++
	return nil
}
func (w *explainExecutionWrapper) ExecForeignInsert(_ context.Context,r catalog.ForeignModifyRequest,state any,row catalog.ForeignRow)(catalog.ForeignRow,bool,error){if err:=w.modify(r,state,row);err!=nil {return nil,false,err};w.data[row[0].(int64)]=row[1].(int64);return row,true,nil}
func (w *explainExecutionWrapper) ExecForeignUpdate(_ context.Context,r catalog.ForeignModifyRequest,state any,id catalog.ForeignRowIdentity,row catalog.ForeignRow)(catalog.ForeignRow,bool,error){if err:=w.modify(r,state,row);err!=nil {return nil,false,err};delete(w.data,id[0].(int64));w.data[row[0].(int64)]=row[1].(int64);return row,true,nil}
func (w *explainExecutionWrapper) ExecForeignDelete(_ context.Context,r catalog.ForeignModifyRequest,state any,id catalog.ForeignRowIdentity,row catalog.ForeignRow)(catalog.ForeignRow,bool,error){if err:=w.modify(r,state,row);err!=nil {return nil,false,err};delete(w.data,id[0].(int64));return row,true,nil}

func explainExecutionFixture(t *testing.T)(*core.Engine,*session.Session,*explainExecutionWrapper){
	e:=engine.New();w:=&explainExecutionWrapper{facts:make(map[*explainExecutionToken]*explainSelectedFacts),data:map[int64]int64{1:10,2:20,3:30}}
	definition,err:=fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName:catalog.ForeignFunctionName{Name:"executed_explain_handler"},Wrapper:w});if err!=nil {t.Fatal(err)}
	if err:=e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition},nil,func(_ context.Context,value driver.Value,usage sqltype.Usage,_ sqltype.InputEnvironment)(sqltype.Value,error){if value==nil {return sqltype.Null(usage.Type),nil};if boolean,ok:=value.(bool);ok {return sqltype.Boolean(usage.Type,boolean),nil};return sqltype.Integer(usage.Type,value.(int64))},func(_ context.Context,value sqltype.Value,_ sqltype.OutputEnvironment)(driver.Value,error){return enginetest.Export(value),nil});err!=nil {t.Fatal(err)}
	s:=enginetest.StartSession(t,e,nil)
	for _,q:=range []string{"CREATE FOREIGN DATA WRAPPER executed_explain HANDLER executed_explain_handler","CREATE SERVER executed_explain_server FOREIGN DATA WRAPPER executed_explain","CREATE USER MAPPING FOR CURRENT_USER SERVER executed_explain_server","CREATE FOREIGN TABLE executed_explain_data(k integer,v integer) SERVER executed_explain_server","CREATE TABLE executed_explain_local(k integer)"} {enginetest.SessionSQL(t,s,q)}
	return e,s,w
}

// Register after StartSession's cleanup, so even a failed assertion cannot
// leave statement admission held while session shutdown waits for it.
func explainExecutionCleanup(t *testing.T, scope *core.StatementScope) {
	t.Helper()
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("explain execution fixture exited before Finish"))
		}
	})
}

func explainExecutionLines(t *testing.T,result core.Result) []string {
	t.Helper();defer result.Rows.Close();values:=make([]sqltype.Value,1);var lines []string
	for {ok,err:=result.Rows.Next(context.Background(),values);if err!=nil {t.Fatal(err)};if !ok {break};lines=append(lines,enginetest.Export(values[0]).(string))};return lines
}

func TestExplainAnalyzeForeignExecutionIdentity(t *testing.T){
	t.Run("ordinary-control",func(t *testing.T){
		e,s,w:=explainExecutionFixture(t)
		w.ordinaryControl,w.declineExecution=true,true
		enginetest.TransactionRows(t,s,"SELECT k FROM executed_explain_data ORDER BY k",1,2,3)
		if w.begins!=1 || w.ends!=1 {t.Fatal("ordinary scan control did not execute and close",w.begins,w.ends)}
		for _,facts:=range w.facts {if facts.explained {t.Fatal("ordinary control captured an analyzed plan")}}
		enginetest.LockEmpty(t,e)
	})
	for _,mode:=range []string{"base-current-params","composite-chosen","composite-declined"} {t.Run(mode,func(t *testing.T){
		e,s,w:=explainExecutionFixture(t);w.declineExecution=mode!="composite-chosen"
		query:="SELECT k FROM executed_explain_data WHERE k=$1";if mode!="base-current-params" {query="SELECT a.k FROM executed_explain_data a JOIN executed_explain_data b ON a.k=b.k WHERE a.k=$1"}
		ast,description:=sequencePreparedDescription(t,e,s,"EXPLAIN (ANALYZE,VERBOSE) "+query)
		analysis,folds,generation:=description.Analysis,description.Folds,description.PlanGeneration;backing:=&description.Results[0];parameter:=&description.Parameters.Types[0]
		var previousLines []string
		var previousText string
		for _,prepared:=range []bool{false,true} {for _,argument:=range []int64{1,2,99,1} {
			op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)};if prepared {scope.Expected=&description}
			explainExecutionCleanup(t,scope)
			w.check=func()error{if !e.Mu.TryLock(){return errors.New("callback holds engine lock")};e.Mu.Unlock();if !e.ReplacementMu.TryLock(){return errors.New("callback holds replacement lock")};e.ReplacementMu.Unlock();w.observation,w.nodes=core.ExplainObservationForTest(scope);return nil}
			imports:=0;before:=len(w.facts)
			result,err:=scope.Execute(ast,1,func(_ context.Context,p analyze.ParameterInfo,frame []sqltype.Value,_ sqltype.InputEnvironment)error{imports++;var err error;frame[0],err=sqltype.Integer(p.Types[0].Type,argument);return err})
			if result.Rows!=nil {defer result.Rows.Close()}
			if err!=nil {scope.Finish(err);t.Fatal(err)}
			if imports!=1 || core.ExplainActiveForTest(scope) {t.Fatal("activation/import lifetime")}
			if err:=scope.Finish(nil);err!=nil {t.Fatal(err)}
			lines:=explainExecutionLines(t,result);want:="rows=1";if argument==99 {want="rows=0"};if !strings.Contains(lines[0],want) || !strings.Contains(lines[0],"loops=1") {t.Fatal(lines)}
			if previousLines!=nil && strings.Join(previousLines,"\n")!=previousText {t.Fatal("next execution changed detached prior text")}
			previousLines,previousText=lines,strings.Join(lines,"\n")
			selected,begun:=0,0;for token,facts:=range w.facts {if token.number<=before {continue};if facts.explained {selected++};if facts.begun {begun++;if !facts.explained {t.Fatal("executed uncaptured plan")};var values []driver.Value;if facts.query!=nil {values=facts.query.Parameters.Values}else {values=facts.base.Parameters.Values};if len(values)!=1 || values[0]!=argument {t.Fatal("stale typed frame",values)}}}
			if selected==0 || selected!=begun || w.begins!=w.ends {t.Fatal("selection/resource imbalance",selected,begun,w.begins,w.ends)}
			composite:=false;for _,node:=range w.nodes {foreign,ok:=w.observation.Foreign(node);if ok && foreign.Composite {composite=true;if w.observation.Actual(node).Loops!=1 {t.Fatal("replaced original not executed")};left,right:=plan.QueryInputs(node);for _,child:=range []plan.Node{left,right} {if child!=nil && w.observation.Actual(child).Loops!=0 {t.Fatal("pushed child falsely executed")}}}}
			if composite!=(mode=="composite-chosen") {t.Fatal("composed renderer lost actual selection",mode,lines)}
			if composite && (!strings.Contains(strings.Join(lines,"\n"),"Foreign operation: join") || !strings.Contains(strings.Join(lines,"\n"),"Execution token:")) {t.Fatal("selected composite properties missing from detached rendering",lines)}
			if mode=="composite-declined" {
				joins,scans:=0,0
				for _,node:=range w.nodes {
					switch node.(type) {
					case *plan.JoinNode: if w.observation.Actual(node).Loops>0 {joins++}
					case *plan.SourceNode: if w.observation.Actual(node).Loops>0 {scans++}
					}
				}
				if joins==0 || scans==0 {t.Fatal("declined composite did not execute original local operators",lines)}
			}
			if description.Analysis!=analysis || description.Folds!=folds || description.PlanGeneration!=generation || &description.Results[0]!=backing || &description.Parameters.Types[0]!=parameter {t.Fatal("retained wrapper changed")}
			frozen:=strings.Join(lines,"\n");for i:=range w.properties {w.properties[i].Value="provider overwritten"};if strings.Join(lines,"\n")!=frozen {t.Fatal("presentation borrowed provider memory")}
			if !reflect.DeepEqual(w.data,map[int64]int64{1:10,2:20,3:30}) {t.Fatal("SELECT mutated provider")};enginetest.LockEmpty(t,e)
		}}
		e.RetirePreparedDescription(&description)
		enginetest.SessionSQL(t,s,"DROP SERVER executed_explain_server CASCADE")
		w.data=nil
		if strings.Join(previousLines,"\n")!=previousText {t.Fatal("source teardown changed detached text")}
	})}
	for _,operation:=range []string{"insert","update","delete"} {t.Run("modify-"+operation,func(t *testing.T){
		for _,returning:=range []bool{false,true} {
			e,s,w:=explainExecutionFixture(t);w.declineExecution=true
			query:="INSERT INTO executed_explain_data VALUES(4,$1)";argument:=int64(40);want:=map[int64]int64{1:10,2:20,3:30,4:40}
			if operation=="update" {query="UPDATE executed_explain_data SET v=$1 WHERE k=2";argument=21;want=map[int64]int64{1:10,2:21,3:30}}
			if operation=="delete" {query="DELETE FROM executed_explain_data WHERE k=$1";argument=3;want=map[int64]int64{1:10,2:20}}
			if returning {query+=" RETURNING k"}
			op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
			explainExecutionCleanup(t,scope)
			imports:=0
			result,err:=scope.Execute(enginetest.Statement(t,"EXPLAIN (ANALYZE,VERBOSE) "+query),1,func(_ context.Context,p analyze.ParameterInfo,frame []sqltype.Value,_ sqltype.InputEnvironment)error{imports++;var err error;frame[0],err=sqltype.Integer(p.Types[0].Type,argument);return err})
			if result.Rows!=nil {defer result.Rows.Close()}
			if err!=nil {scope.Finish(err);t.Fatal(err)};if err:=scope.Finish(nil);err!=nil {t.Fatal(err)}
			if imports!=1 {t.Fatal("modification reimported frame",imports)}
			for _,facts:=range w.facts {if facts.modify!=nil && (len(facts.modify.Parameters.Values)!=1 || facts.modify.Parameters.Values[0]!=argument) {t.Fatal("modification selection lost current typed arguments",facts.modify.Parameters)}}
			lines:=explainExecutionLines(t,result);count:="rows=0";if returning {count="rows=1"};if !strings.Contains(lines[0],count) || !reflect.DeepEqual(w.data,want) || w.modifies!=1 || w.modifyBegins!=1 || w.modifyEnds!=1 || w.begins!=w.ends {t.Fatal(lines,w.data,w.modifies,w.modifyBegins,w.modifyEnds)}
			wantScans:=1;if operation=="insert" {wantScans=0}
			if w.begins!=wantScans || w.ends!=wantScans {t.Fatal("identity scan did not close exactly once",w.begins,w.ends)}
			enginetest.LockEmpty(t,e)
		}
	})}
	t.Run("modifying-cte-command-identity",func(t *testing.T){
		e,s,w:=explainExecutionFixture(t);w.declineExecution=true
		op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
		explainExecutionCleanup(t,scope)
		query:="EXPLAIN (ANALYZE,VERBOSE) WITH changed AS (UPDATE executed_explain_data SET v=21 WHERE k=2 RETURNING k) INSERT INTO executed_explain_data VALUES(4,40) RETURNING k"
		result,err:=scope.Execute(enginetest.Statement(t,query),0,nil)
		if result.Rows!=nil {defer result.Rows.Close()}
		if err!=nil {scope.Finish(err);t.Fatal(err)};if err:=scope.Finish(nil);err!=nil {t.Fatal(err)}
		lines:=explainExecutionLines(t,result)
		tokens:=make(map[string]bool)
		for _,line:=range lines {if at:=strings.Index(line,"Execution token:");at>=0 {tokens[line[at:]]=true}}
		if len(tokens)!=2 || w.modifies!=2 || w.modifyBegins!=2 || w.modifyEnds!=2 || !reflect.DeepEqual(w.data,map[int64]int64{1:10,2:21,3:30,4:40}) {t.Fatal("command-local modification properties overwritten",lines,w.data)}
		if w.begins!=1 || w.ends!=1 {t.Fatal("CTE identity scan did not close exactly once",w.begins,w.ends)}
		enginetest.LockEmpty(t,e)
	})
	t.Run("begin-frame-error",func(t *testing.T){
		e,s,w:=explainExecutionFixture(t);w.declineExecution=true
		op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
		explainExecutionCleanup(t,scope)
		// Corrupt only the fixture's saved expectation after the callback has
		// checked it. Begin must diagnose the mismatch through Execute/Finish,
		// not Goexit while the statement still holds admission.
		w.check=func()error{
			for _,facts:=range w.facts {if facts.explained {facts.base.Parameters.Values[0]=int64(-43)}}
			return nil
		}
		result,err:=scope.Execute(enginetest.Statement(t,"EXPLAIN (ANALYZE,VERBOSE) SELECT k FROM executed_explain_data WHERE k=$1"),1,func(_ context.Context,p analyze.ParameterInfo,frame []sqltype.Value,_ sqltype.InputEnvironment)error{
			var err error;frame[0],err=sqltype.Integer(p.Types[0].Type,1);return err
		})
		if result.Rows!=nil {defer result.Rows.Close()}
		if err==nil || !strings.Contains(err.Error(),"base Begin did not use captured plan/frame") || result.Rows!=nil {t.Fatal("provider assertion did not return its diagnostic",result,err)}
		if finishErr:=scope.Finish(err);!errors.Is(finishErr,err) {t.Fatal("Finish lost provider assertion",finishErr,err)}
		if !scope.Finished || scope.View!=nil || scope.LockTicket!=nil || core.ExplainActiveForTest(scope) || w.begins!=0 || w.ends!=0 {t.Fatal("provider assertion retained execution owners")}
		enginetest.LockEmpty(t,e)
		enginetest.SessionSQL(t,s,"SELECT 1")
	})
	for _,mode:=range []string{"callback-error","invalid-property","callback-cancel","begun-cancel","finish-veto","callback-error-explicit","invalid-property-explicit","callback-cancel-explicit","modify-callback-error","modify-invalid-property","modify-callback-cancel"} {t.Run(mode,func(t *testing.T){
		modification:=strings.HasPrefix(mode,"modify-")
		mode=strings.TrimPrefix(mode,"modify-")
		explicit:=strings.HasSuffix(mode,"-explicit")
		mode=strings.TrimSuffix(mode,"-explicit")
		e,s,w:=explainExecutionFixture(t);w.declineExecution=true
		if explicit {enginetest.SessionSQL(t,s,"BEGIN")}
		op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil {t.Fatal(err)}
		explainExecutionCleanup(t,scope)
		base,cancel:=context.WithCancel(op.Context());defer cancel();op.Ctx=base
		sentinel:=errors.New("executed explain property failure")
		callbacks:=0
		w.check=func()error{
			callbacks++
			if !e.Mu.TryLock() {return errors.New("callback holds engine lock")};e.Mu.Unlock()
			if !e.ReplacementMu.TryLock() {return errors.New("callback holds replacement lock")};e.ReplacementMu.Unlock()
			if observation,_:=core.ExplainObservationForTest(scope);observation==nil {return errors.New("callback lost analyzed command owner")}
			return nil
		}
		switch mode {case "callback-error":w.failure=sentinel;case "invalid-property":w.invalid=true;case "callback-cancel":w.cancel=cancel;case "begun-cancel":w.cancelFetch=cancel}
		query:="INSERT INTO executed_explain_local SELECT k FROM executed_explain_data"
		if modification {query="INSERT INTO executed_explain_data VALUES(4,40)"}
		result,err:=scope.Execute(enginetest.Statement(t,"EXPLAIN (ANALYZE,VERBOSE) "+query),0,nil)
		if result.Rows!=nil {defer result.Rows.Close()}
		if mode=="finish-veto" {
			if err!=nil || result.Rows==nil {scope.Finish(err);t.Fatal(err)}
			if len(scope.Stage.Rows)==0 || core.ExplainActiveForTest(scope) {t.Fatal("veto fixture lacks staged real child work")}
			op.Ctx=&cancelAtErrCheck{Context:base,cancel:cancel,at:2};err=scope.Finish(nil);result.Rows.Close();if !errors.Is(err,context.Canceled) {t.Fatal(err)}
		} else {
			executionErr:=err
			finishErr:=scope.Finish(executionErr)
			if result.Rows!=nil || executionErr==nil {t.Fatal("callback failure exposed successful plan",result,executionErr)}
			if mode=="callback-error" && !errors.Is(executionErr,sentinel) {t.Fatal(executionErr)}
			if mode=="invalid-property" && executionErr.Error()!="stuntgres: invalid foreign EXPLAIN property" {t.Fatal("invalid property diagnostic changed",executionErr)}
			if strings.Contains(mode,"cancel") && !errors.Is(executionErr,context.Canceled) {t.Fatal(executionErr)}
			if !errors.Is(finishErr,executionErr) {t.Fatal("Finish lost callback failure",finishErr,executionErr)}
		}
		if callbacks!=1 || !scope.Finished {t.Fatal("fixture missed actual callback/Finish boundary",callbacks,scope.Finished)}
		if core.ExplainActiveForTest(scope) || scope.View!=nil || scope.LockTicket!=nil || w.begins!=w.ends || (mode!="begun-cancel" && mode!="finish-veto" && w.begins!=0) {t.Fatal("callback failure retained or began execution owners",w.begins,w.ends)}
		if mode=="begun-cancel" && (w.begins!=1 || !slices.Contains(w.tx.calls,"statement-failure") || !slices.Contains(w.tx.calls,"rollback")) {t.Fatal("begun scan participant not settled",w.begins,w.tx.calls)}
		if modification && (w.modifyBegins!=0 || w.modifies!=0 || w.modifyEnds!=0 || !reflect.DeepEqual(w.data,map[int64]int64{1:10,2:20,3:30})) {t.Fatal("failed explain callback entered foreign modification")}
		if explicit {
			nextOp:=enginetest.TransactionOperation(t,s)
			next,err:=e.StartStatement(nextOp)
			if err==nil {explainExecutionCleanup(t,next);_,err=next.Execute(enginetest.Statement(t,"SELECT 1"),0,nil);_ = next.Finish(err)} else {nextOp.Done()}
			enginetest.State(t,err,"25P02")
			enginetest.SessionSQL(t,s,"ROLLBACK")
		}
		enginetest.TransactionRows(t,s,"SELECT k FROM executed_explain_local")
		enginetest.LockEmpty(t,e)
	})}
}
