package core_test

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type fdwExplainContextProbe struct {
	explainBoundaryWrapper
	upper int
	decline bool
	queries []catalog.ForeignQueryRequest
	bases []catalog.ForeignRequest
}

func (w *fdwExplainContextProbe) GetForeignUpperPaths(_ context.Context, request catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath,error) {
	w.upper++
	if w.decline { return nil,nil }
	path := catalog.ForeignQueryPath{TotalCost:1,Compatibility:catalog.ForeignQueryCompatibility{Expressions:true,Collations:true,StatementVisibility:true},Plan:catalog.ForeignQueryPlan{Slots:slices.Clone(request.Stage.Slots)}}
	request.Sources[0].Request.Columns[0].Name = "discovery mutation"
	return []catalog.ForeignQueryPath{path},nil
}
func (w *fdwExplainContextProbe) BeginForeignQueryScan(context.Context,catalog.ForeignQueryRequest,catalog.ForeignQueryPlan) (catalog.ForeignScan,error) {
	w.entered = true
	return nil,explainProviderExecuted
}
func (w *fdwExplainContextProbe) ExplainForeignScan(_ context.Context, request catalog.ForeignExplainScanRequest) ([]catalog.ForeignExplainProperty,error) {
	if request.Query != nil {
		w.queries = append(w.queries,exec.CopyForeignQueryRequest(request.Query.Request))
		request.Query.Request.Sources[0].Request.Columns[0].Name = "explain mutation"
		request.Query.Path.Plan.Slots[0].TypeName = "explain mutation"
	} else {
		w.bases = append(w.bases,exec.CopyForeignRequest(request.ForeignRequest))
		request.Columns[0].Name = "base explain mutation"
	}
	return []catalog.ForeignExplainProperty{{Name:"Provider plan",Value:"owned"}},nil
}

func TestFDWExplainContextDemandOwnership(t *testing.T) {
	e := engine.New()
	w := &fdwExplainContextProbe{}
	definition,err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName:catalog.ForeignFunctionName{Name:"explain_context_handler"},Wrapper:w})
	if err != nil { t.Fatal(err) }
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition},nil,
		func(_ context.Context,value driver.Value,usage sqltype.Usage,_ sqltype.InputEnvironment)(sqltype.Value,error){ if value == nil { return sqltype.Null(usage.Type),nil }; return sqltype.Integer(usage.Type,value.(int64)) },
		func(_ context.Context,value sqltype.Value,_ sqltype.OutputEnvironment)(driver.Value,error){ return enginetest.Export(value),nil }); err != nil { t.Fatal(err) }
	s := enginetest.StartSession(t,e,nil)
	for _, query := range []string{
		"CREATE FOREIGN DATA WRAPPER explain_context_fdw HANDLER explain_context_handler",
		"CREATE SERVER explain_context_server FOREIGN DATA WRAPPER explain_context_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER explain_context_server",
		"CREATE FOREIGN TABLE explain_context_data(k integer,v integer) SERVER explain_context_server",
	} { enginetest.SessionSQL(t,s,query) }
	for _, tc := range []struct{ query string; composite,decline bool }{
		{"SELECT count(*) AS n FROM explain_context_data",true,false},
		{"SELECT (SELECT count(*) FROM explain_context_data) AS n",false,false},
		{"SELECT count(*) AS n FROM explain_context_data",false,true},
	} {
		w.decline = tc.decline
		ast,description := sequencePreparedDescription(t,e,s,"EXPLAIN (VERBOSE,COSTS false) "+tc.query)
		analysis,folds,generation := description.Analysis,description.Folds,description.PlanGeneration
		statement := enginetest.Statement(t,tc.query)
		op,err := s.Acquire(context.Background()); if err != nil { t.Fatal(err) }
		scope := core.NewStatementScope(op,s.Transactions,op.StatementStart())
		e.Mu.Lock()
		auth,err := e.AuthorizationLocked(op)
		if err != nil { e.Mu.Unlock(); op.Done(); t.Fatal(err) }
		binding,err := core.Describe(op.Ctx,e.AnalysisHost(op),statement,auth,ids.ObjectRef{},sqltype.InputEnvironment{},nil,nil)
		e.Mu.Unlock()
		if err != nil { op.Done(); t.Fatal(err) }
		if len(binding.QuerySources) != 1 || binding.QuerySources[0].Uses != nil { op.Done(); t.Fatal("fixture lost original nil bound demand",binding.QuerySources) }
		root := binding.Query.Root
		for range 2 {
			upper := w.upper
			e.Mu.Lock()
			lines,err := core.ExplainBoundForTest(scope,auth,statement,binding)
			e.Mu.Unlock()
			if err != nil { op.Done(); t.Fatal(err) }
			if binding.Query.Root != root || binding.QuerySources[0].Uses != nil || w.entered || !strings.Contains(strings.Join(lines,"\n"),"Provider plan: owned") { op.Done(); t.Fatal("renderer mutated bound owner/demand or entered execution") }
			if tc.composite {
				request := w.queries[len(w.queries)-1]
				if w.upper <= upper || request.Sources[0].Request.RequiredAttributes == nil || len(request.Sources[0].Request.RequiredAttributes) != 0 || request.Sources[0].Request.Columns[0].Name != "k" { op.Done(); t.Fatal("selected source request borrowed callback memory",request) }
				source := request.Stage.Children[0]
				if source.Kind != "source" || len(source.Slots) < 2 || !source.Slots[0].Unused || !source.Slots[1].Unused { op.Done(); t.Fatal("retained presence source layout was widened",source) }
			} else {
				request := w.bases[len(w.bases)-1]
				if request.RequiredAttributes == nil || len(request.RequiredAttributes) != 0 || request.Columns[0].Name != "k" || !tc.decline && w.upper != upper { op.Done(); t.Fatal("base demand or nested eligibility diverged",request) }
			}
		}
		op.Done()
		for range 2 {
			result,err := sequencePreparedExecute(e,s,ast,&description)
			if err != nil { t.Fatal(err) }; result.Rows.Close()
			if description.Analysis != analysis || description.Folds != folds || description.PlanGeneration != generation || folds.Live() || folds.Current().Generation != 0 || len(folds.Current().Entries) != 0 { t.Fatal("EXPLAIN replaced its accepted definition owner/generation") }
		}
		e.RetirePreparedDescription(&description)
	}
}
