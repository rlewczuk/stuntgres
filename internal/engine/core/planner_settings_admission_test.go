package core_test

import (
	"context"
	"errors"
	"testing"
	"runtime"
	"strings"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func plannerStoreValue(store *session.SettingsStore, on bool) {
	store.Mu.Lock()
	defer store.Mu.Unlock()
	for _, name := range []string{"enable_hashjoin","enable_hashagg","enable_indexscan","enable_partition_pruning","enable_gathermerge"} {
		store.Typed[name] = settings.Value{Boolean:on}
		value := "off"; if on { value="on" }; store.Values[name]=value
	}
}
func TestPlannerSettingsAdmissionSnapshots(t *testing.T) {
	e:=engine.New()
	sess:=enginetest.StartSession(t,e,nil)
	for _, q := range plannerAdmissionSentinelSQL {
		plannerCostCommand(t, sess, q)
	}
	op,_,err:=sess.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }
	defer op.Done()
	original:=op.PlannerMethods
	assertPlannerAdmissionSentinels(t, op)
	plannerStoreValue(sess.Settings,false)
	if op.PlannerMethods!=original || original!=(plan.PlannerMethods{}) { t.Fatal("operation retained live settings",op.PlannerMethods) }
	scope:=core.NewStatementScope(op,sess.Transactions,op.StatementStart())
	if scope.PlannerMethods!=original { t.Fatal("scope reread live session") }
	state:=exec.State{Source:&exec.Source{Host:core.HostOf(scope)}}
	state.CapturePlannerAdmission()
	scope.PlannerMethods=plan.PlannerMethods{NoHashJoin:true}
	state.CapturePlannerAdmission()
	if state.Methods!=original { t.Fatal("retained state reread rebound host") }
	// An effective detached invocation must not consume the session-only op.
	op.Restricted=&session.RestrictedInvocation{}
	detached:=op.EffectiveSettings()
	plannerStoreValue(detached,true)
	inner:=core.NewStatementScope(op,sess.Transactions,op.StatementStart())
	if inner.PlannerMethods!=(plan.PlannerMethods{}) { t.Fatal("detached effective settings ignored") }
	plannerStoreValue(detached,false)
	later:=core.NewStatementScope(op,sess.Transactions,op.StatementStart())
	allOff:=plan.PlannerMethods{NoHashJoin:true,NoHashAgg:true,NoIndexScan:true,NoPartitionPruning:true,NoGatherMerge:true}
	if later.PlannerMethods!=allOff || inner.PlannerMethods!=(plan.PlannerMethods{}) { t.Fatal("nested SQL snapshot lifetime") }
	assertPlannerAdmissionSentinels(t, op)
	saved:=later.PlannerMethods
	restore:=later.ActivateSQLRoutine(nil,catalog.Read{})
	later.PlannerMethods=plan.PlannerMethods{}
	if err:=restore(nil); err!=nil || later.PlannerMethods!=saved { t.Fatal("routine scratch did not restore immutable methods",err) }
	assertPlannerAdmissionSentinels(t, op)
	op.Restricted=nil
	op.Done()
	next,_,err:=sess.AcquireCommand(context.Background()); if err!=nil { t.Fatal(err) }; defer next.Done()
	if next.PlannerMethods!=allOff { t.Fatal("new command missed successful setting") }

	// Exercise the real lazy cursor installation/detachment boundary with a
	// typed tree. Its executor owner is the same sqlCursor used by portals.
	ce,cs:=cursorUnitScope(t)
	integer,_:=ce.CatalogRead().LookupType(sqltype.Int4)
	installPlannerAdmissionSentinels(cs.Op)
	seven,_:=sqltype.Integer(integer,7)
	root:=&plan.ProjectNode{QuerySchema:[]catalog.Column{{Name:"k",Usage:integer.Usage()}},Child:&plan.SingletonNode{},Expressions:[]expr.Expr{plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:seven}}}
	core.SetScopeAuthority(cs,&exec.Authority{Host:core.HostOf(cs)})
	cs.Inquiry.CommentCatalog=ce.CatalogRead()
	cs.PlannerMethods=allOff
	source:=&exec.Source{Singleton:true,Locks:&exec.LockContext{},Host:core.HostOf(cs),PlannerCosts:cs.Op.PlannerCosts}
	declaration:=&parser.DeclareCursor{Name:parser.Identifier{Text:"pm_owned"}}
	cursor,_,err:=core.ScopeInstallCursor(cs,declaration,&declaration.Name.Text,root,source,nil,true)
	if err!=nil { t.Fatal(err) }
	cs.PlannerMethods=plan.PlannerMethods{}
	retained:=core.CursorExecutionState(cursor)
	retained.CapturePlannerAdmission()
	if retained.Methods!=allOff { t.Fatal("cursor reread declaration owner after detachment",retained.Methods) }
	assertPlannerAdmissionSentinels(t, cs.Op)
	if retained.Source != source || source.PlannerCosts.Tuple != 1.25 || source.ScanFraction != .75 {
		t.Fatal("cursor replaced independent admitted costs/fraction", source)
	}
	cs.Op.PlannerCosts.Tuple, cs.Op.CursorTupleFraction = 9, .25
	retained.CapturePlannerAdmission()
	if source.PlannerCosts.Tuple != 1.25 || source.ScanFraction != .75 || retained.Methods != allOff {
		t.Fatal("cursor recaptured rebound operation authority")
	}
}

func TestPlannerSettingsPortalSnapshots(t *testing.T) {
	e,s:=cursorUnitScope(t)
	installPlannerAdmissionSentinels(s.Op)
	integer,_:=e.CatalogRead().LookupType(sqltype.Int4)
	seven,_:=sqltype.Integer(integer,7)
	root:=&plan.ProjectNode{QuerySchema:[]catalog.Column{{Name:"k",Usage:integer.Usage()}},Child:&plan.SingletonNode{},Expressions:[]expr.Expr{plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:seven}}}
	methods:=plan.PlannerMethods{NoHashAgg:true,NoHashJoin:true,NoPartitionPruning:true,NoIndexScan:true,NoGatherMerge:true}
	s.PlannerMethods=methods
	core.SetScopeAuthority(s,&exec.Authority{Host:core.HostOf(s)})
	s.FoldCaptures=&exec.FoldCaptureCandidate{Sealed:true}
	portal:=core.NewProtocolPortal(core.PreparedDefinition{},s.TxRef)
	core.SetProtocolScopeBind(core.ScopeProtocol(s),portal)
	source:=&exec.Source{Singleton:true,Host:core.HostOf(s),PlannerCosts:s.Op.PlannerCosts}
	if _,err:=core.ScopeInstallProtocolProducer(s,root,source,nil); err!=nil { t.Fatal(err) }
	defer core.PortalClose(portal,context.Background())
	producer:=*core.PortalProducerRef(portal)
	if producer==nil { t.Fatal("no actual retained producer") }
	s.PlannerMethods=plan.PlannerMethods{}
	state:=core.CursorExecutionState(producer)
	state.CapturePlannerAdmission()
	if state.Methods!=methods { t.Fatal("portal borrowed later methods",state.Methods) }
	if core.CursorAuthority(producer).Host!=nil { t.Fatal("portal retained scope authority") }
	assertPlannerAdmissionSentinels(t, s.Op)
	if state.Source != source || source.PlannerCosts.Tuple != 1.25 {
		t.Fatal("portal replaced independent admitted costs", source)
	}
	s.Op.PlannerCosts.Tuple, s.Op.CursorTupleFraction = 9, .25
	state.CapturePlannerAdmission()
	if source.PlannerCosts.Tuple != 1.25 || state.Methods != methods {
		t.Fatal("portal recaptured rebound operation authority")
	}
}

var plannerAdmissionSentinelSQL = []string{
	"SET cpu_tuple_cost='1.25'",
	"SET constraint_exclusion=off",
	"SET cursor_tuple_fraction='0.75'",
	"SET recursive_worktable_factor=7",
	"SET default_statistics_target=321",
}

func installPlannerAdmissionSentinels(op *session.Operation) {
	op.PlannerCosts.Tuple = 1.25
	op.ConstraintExclusion = "off"
	op.CursorTupleFraction = .75
	op.RecursiveWorktableFactor = 7
	op.StatisticsTarget = 321
}

func assertPlannerAdmissionSentinels(t *testing.T, op *session.Operation) {
	t.Helper()
	if op.PlannerCosts.Tuple != 1.25 || op.ConstraintExclusion != "off" ||
		op.CursorTupleFraction != .75 || op.RecursiveWorktableFactor != 7 || op.StatisticsTarget != 321 {
		t.Fatal("method admission replaced independent COSTS/OTHER authority", op.PlannerCosts,
			op.ConstraintExclusion, op.CursorTupleFraction, op.RecursiveWorktableFactor, op.StatisticsTarget)
	}
}

// Observe real provider execution through the existing context.Err seam,
// without replacing providers or exporting production inspection hooks.
type plannerRoutineContext struct {
	context.Context
	observe func(hash, equality bool)
}

func (c *plannerRoutineContext) Err() error {
	var pcs [96]uintptr
	n := runtime.Callers(1, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	hash, equality := false, false
	for {
		frame, more := frames.Next()
		hash = hash || strings.Contains(frame.Function, "sqltype.Grouping.Hash")
		equality = equality || strings.Contains(frame.Function, "exec.findEqualityQueryGroup")
		if !more { break }
	}
	if c.observe != nil && (hash || equality) { c.observe(hash, equality) }
	return c.Context.Err()
}

func TestPlannerSettingsRoutineAdmission(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	body := " DECLARE a bigint; b bigint; BEGIN SELECT count(*) INTO a FROM (SELECT k,count(*) FROM pm_routine GROUP BY k) g; PERFORM set_config('enable_hashagg','on',false); EXECUTE 'SELECT count(*) FROM (SELECT k,count(*) FROM pm_routine GROUP BY k) g' INTO b; "
	for _, q := range append([]string{
		"CREATE TABLE pm_routine(k integer)",
		"INSERT INTO pm_routine VALUES(1),(1),(2),(NULL)",
		"CREATE FUNCTION pm_routine_sql() RETURNS bigint LANGUAGE SQL SET enable_hashagg=off AS $$ SELECT count(*) FROM (SELECT k,count(*) FROM pm_routine GROUP BY k) g $$",
		"CREATE FUNCTION pm_routine_ok() RETURNS bigint LANGUAGE plpgsql SET enable_hashagg=off AS $$" + body + "RETURN a*10+b; END $$",
		"CREATE FUNCTION pm_routine_fail() RETURNS bigint LANGUAGE plpgsql SET enable_hashagg=off AS $$" + body + "RAISE EXCEPTION 'planner grouped failure'; END $$",
	}, plannerAdmissionSentinelSQL...) { plannerCostCommand(t, s, q) }
	for _, name := range []string{"pm_routine_sql", "pm_routine_ok", "pm_routine_fail"} {
		t.Run(name, func(t *testing.T) {
			op, _, err := s.AcquireCommand(context.Background())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			// AcquireCommand installs a cancelCtx, whose Err does not poll
			// its parent's Err. Observe the actual execution context after
			// acquisition, preserving its real cancellation authority.
			ctx := &plannerRoutineContext{Context:op.Context()}
			op.Ctx, op.CommandContext = ctx, ctx
			scope, err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			// Assertions inside the provider observer can call Fatal/Goexit.
			// Release the real statement admission before the next subtest,
			// including that path; disable observation before cleanup executes.
			defer func() {
				ctx.observe = nil
				if !scope.Finished {
					_ = scope.Finish(errors.New("routine admission fixture aborted"))
				}
			}()
			outer := scope.PlannerMethods
			parent := &exec.State{Source:&exec.Source{Host:core.HostOf(scope),PlannerCosts:op.PlannerCosts}}
			parent.CapturePlannerAdmission()
			equalitySeen, changedHashSeen := false, false
			ctx.observe = func(hash, equality bool) {
				assertPlannerAdmissionSentinels(t, op)
				if parent.Methods != outer || parent.Source.PlannerCosts != op.PlannerCosts {
					t.Fatal("nested admission changed retained outer execution")
				}
				if equality {
					equalitySeen = true
					if !scope.PlannerMethods.NoHashAgg { t.Fatal("effective routine off was not admitted") }
				}
				if hash {
					changedHashSeen = true
					if scope.PlannerMethods.NoHashAgg { t.Fatal("later SQL ignored successful set_config") }
				}
			}
			result, runErr := scope.Execute(enginetest.Statement(t, "SELECT "+name+"()"), 0, nil)
			ctx.observe = nil
			if result.Rows != nil { result.Rows.Close() }
			if (runErr != nil) != (name == "pm_routine_fail") { t.Fatal("routine outcome", runErr) }
			if runErr != nil {
				var diagnostic interface{ SQLState() string }
				if !errors.As(runErr, &diagnostic) || diagnostic.SQLState() != "P0001" {
					t.Fatal("wrong routine failure", runErr)
				}
			}
			if !equalitySeen || changedHashSeen != (name != "pm_routine_sql") {
				t.Fatal("actual routine grouping paths missing", equalitySeen, changedHashSeen)
			}
			if scope.PlannerMethods != outer || parent.Methods != outer || op.PlannerMethods != outer {
				t.Fatal("success/error unwinding replaced enclosing method owner")
			}
			assertPlannerAdmissionSentinels(t, op)
			if value, _ := op.Setting("enable_hashagg"); value != "on" { t.Fatal("routine SET frame escaped", value) }
			// A derived execution opened from the retained parent must not
			// borrow the opposite method now installed on its lexical host.
			integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
			one, _ := sqltype.Integer(integer, 1)
			two, _ := sqltype.Integer(integer, 2)
			parent.Source.Anonymous = [][]sqltype.Value{{one},{one},{two},{sqltype.Null(integer)}}
			grouping, bindErr := sqltype.BindGrouping(integer.Usage())
			if bindErr != nil { t.Fatal(bindErr) }
			schema := plan.QuerySchema{{Name:"k",Usage:integer.Usage()}}
			column := plan.ColumnExpr{Info:expr.Info{Usage:integer.Usage(),Property:expr.Properties{RowDependent:true}},Scope:1,Frame:1,Slot:0}
			distinct := &plan.DistinctNode{QuerySchema:schema,Child:&plan.SourceNode{QuerySchema:schema},Expressions:[]expr.Expr{column},Grouping:[]plan.Grouping{{Grouping:grouping}}}
			project := &plan.ProjectNode{QuerySchema:schema,Child:distinct,Frame:1,Expressions:[]expr.Expr{column}}
			scope.PlannerMethods = plan.PlannerMethods{NoHashAgg:true}
			childHash := false
			childCtx := &plannerRoutineContext{Context:context.Background(),observe:func(hash, equality bool) {
				childHash = childHash || hash
				if equality { t.Fatal("child borrowed rebound scope method") }
			}}
			child, childErr := exec.OpenDerivedNode(childCtx, parent, &plan.DerivedNode{QuerySchema:schema,Query:&plan.Query{Root:project}})
			if childErr != nil { t.Fatal(childErr) }
			childErr = child.Drain(childCtx)
			if childErr != nil || child.Count() != 3 || !childHash || parent.Methods != outer {
				t.Fatal("retained outer/child algorithm", childErr, child.Count(), childHash, parent.Methods)
			}
			child.Close()
			scope.PlannerMethods = outer
			finishErr := scope.Finish(runErr)
			if runErr == nil && finishErr != nil { t.Fatal(finishErr) }
		})
	}
}
