package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These providers wrap the established real foreign planning/transaction seams.
// The callbacks observe the executing owners, not fabricated diagnostics output.
type diagnosticsLiveProvider struct {
	fdwUnitScanWrapper
	scan    *diagnosticsLiveScan
	tx      *plExceptionFaultTx
	observe func(string)
}

func (*diagnosticsLiveProvider) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{}}, nil
}
func (p *diagnosticsLiveProvider) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	return p.tx, nil
}
func (p *diagnosticsLiveProvider) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	p.observe("begin")
	return p.scan, nil
}

type diagnosticsLiveScan struct {
	fdwUnitScan
	observe func(string)
	primary error
	cancel  context.CancelFunc
}

func (s *diagnosticsLiveScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	s.observe("next")
	s.position++
	if s.cancel != nil {
		s.cancel()
		return nil, false, ctx.Err()
	}
	if s.primary != nil {
		return nil, false, s.primary
	}
	return catalog.ForeignRow{}, s.position == 1, nil
}

func TestPLpgSQLDiagnosticsLiveStackOwnership(t *testing.T) {
	for _, site := range []struct{ name, result, body, action string; minSites int }{
		{"initializer", "integer", "DECLARE x integer := (SELECT 7 FROM diagnostics_live_target); BEGIN RETURN x; END", "during statement block local variable initialization", 3},
		{"static_capture", "integer", "DECLARE x integer; BEGIN SELECT 7 INTO x FROM diagnostics_live_target; RETURN x; END", "at SQL statement", 2},
		{"static_perform", "integer", "BEGIN PERFORM 7 FROM diagnostics_live_target; RETURN 7; END", "at PERFORM", 2},
		{"dynamic_execute", "integer", "DECLARE x integer; BEGIN EXECUTE 'SELECT 7 FROM diagnostics_live_target' INTO x; RETURN x; END", "at EXECUTE", 2},
		{"dynamic_command_expression", "integer", "DECLARE x integer; BEGIN EXECUTE (SELECT 'SELECT 7'::text FROM diagnostics_live_target) INTO x; RETURN x; END", "at EXECUTE", 3},
		{"dynamic_using_expression", "integer", "DECLARE x integer; BEGIN EXECUTE 'SELECT $1::integer' INTO x USING (SELECT 7 FROM diagnostics_live_target); RETURN x; END", "at EXECUTE", 3},
		{"dynamic_return_query", "SETOF integer", "BEGIN RETURN QUERY EXECUTE 'SELECT 7 FROM diagnostics_live_target'; END", "at RETURN QUERY", 2},
		{"dynamic_return_query_command", "SETOF integer", "BEGIN RETURN QUERY EXECUTE (SELECT 'SELECT 7'::text FROM diagnostics_live_target); END", "at RETURN QUERY", 3},
		{"dynamic_return_query_using", "SETOF integer", "BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer' USING (SELECT 7 FROM diagnostics_live_target); END", "at RETURN QUERY", 3},
	} {
	for _, outcome := range []string{"success", "sql_error", "ordinary_error", "cancel", "close_fault", "primary_close_fault"} {
		t.Run(site.name+"/"+outcome, func(t *testing.T) {
			e := engine.New()
			primary := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero", Detail: "diagnostics producer primary"}
			ordinary := errors.New("diagnostics ordinary producer failure")
			secondary := errors.New("diagnostics producer End failure")
			scan := &diagnosticsLiveScan{}
			provider := &diagnosticsLiveProvider{scan: scan, tx: &plExceptionFaultTx{}}
			wrapper, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "diagnostics_live_handler"}, Wrapper: provider})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{wrapper}, nil,
				func(_ context.Context, v driver.Value, u sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
					if v == nil {
						return sqltype.Null(u.Type), nil
					}
					return sqltype.Integer(u.Type, v.(int64))
				},
				func(_ context.Context, v sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
					if v.IsNull() {
						return nil, nil
					}
					return v.Integer()
				},
			); err != nil {
				t.Fatal(err)
			}
			owner := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE FOREIGN DATA WRAPPER diagnostics_live HANDLER diagnostics_live_handler",
				"CREATE SERVER diagnostics_live_server FOREIGN DATA WRAPPER diagnostics_live",
				"CREATE USER MAPPING FOR CURRENT_USER SERVER diagnostics_live_server",
				"CREATE FOREIGN TABLE diagnostics_live_target(v integer) SERVER diagnostics_live_server",
				"CREATE FUNCTION pg_catalog.diagnostics_live_leaf() RETURNS "+site.result+" LANGUAGE plpgsql AS $$"+site.body+"$$",
				`CREATE FUNCTION pg_catalog.diagnostics_live_sql() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT 1; SELECT pg_catalog.diagnostics_live_leaf()'`,
				`CREATE FUNCTION pg_catalog.diagnostics_live_outer() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN pg_catalog.diagnostics_live_sql(); END$$`,
				`CREATE FUNCTION pg_catalog.diagnostics_reentry(n integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; c bigint; o oid; stack text; BEGIN PERFORM 1 UNION ALL SELECT 2 UNION ALL SELECT 3; IF n>0 THEN x:=pg_catalog.diagnostics_reentry(n-1); END IF; GET DIAGNOSTICS c=ROW_COUNT,o=PG_ROUTINE_OID,stack=PG_CONTEXT; PERFORM 1 FROM diagnostics_live_target; RETURN c; END$$`,
			} {
				enginetest.SessionSQL(t, owner, query)
			}
			enginetest.TransactionBegin(t, owner)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := owner.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			s, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Finish(context.Canceled)
			// Install genuine enclosing admission, retained snapshot and authority.
			ast := enginetest.Statement(t, "SELECT 1")
			description, err := s.Describe(ast)
			if err != nil {
				t.Fatal(err)
			}
			s.Expected = &description
			result, err := s.Execute(ast, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, len(result.Rows.Schema()))
			for {
				ok, err := result.Rows.Next(op.Context(), row)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
			}
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			read := e.CatalogRead()
			var outer, leaf *catalog.Procedure
			for candidate := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "diagnostics_live_outer") {
				outer = candidate
			}
			for candidate := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "diagnostics_live_leaf") {
				leaf = candidate
			}
			if outer == nil || leaf == nil || outer.RoutineOID() == 0 || leaf.RoutineOID() == 0 || outer.RoutineOID() == leaf.RoutineOID() {
				t.Fatal("missing distinct real declaration identities")
			}
			baselineSite := &parser.Select{}
			restoreEnclosing := core.ScopeDiagnosticsEnclosing(s, outer.SQL, baselineSite)
			defer restoreEnclosing()
			outerLexical := &proc.PLFrame{}
			core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(s), outerLexical)
			outerOwner := &exec.ForeignScanOwner{}
			core.SetForeignTableScopeOwner(core.ScopeForeignTables(s), outerOwner)
			authority := core.ScopeAuthority(s)
			if authority == nil || authority.Host == nil {
				t.Fatal("no admitted authority")
			}
			s.Completed = true
			core.SetScopeEffectCommand(s, 93)
			beforeLive, beforeSites := core.ScopeDiagnosticsLive(s), core.ScopeDiagnosticsExpressionSites(s)
			beforeDemand, beforeIdentity, beforeExpected := core.ScopeSQLRoutine(s), s.Identity, s.Expected
			beforeDepth, beforeSessionCompleted := core.ScopeActiveSQLRoutineDepth(s), core.ScopeSessionCompleted(s)
			var events []string
			var retainedText string
			var activeFrame *proc.PLFrame
			var queryNode any
			unlocked := plExceptionUnlocked(t, e, s)
			observe := func(event string) {
				unlocked()
				events = append(events, event)
				live := core.ScopeDiagnosticsLive(s)
				executing := site.name == "dynamic_execute" || site.name == "dynamic_return_query" || site.name == "static_capture" || site.name == "static_perform"
				command := "SELECT 7 FROM diagnostics_live_target"
				if site.name == "static_capture" {
					command = "SELECT 7        FROM diagnostics_live_target"
				}
				if executing {
					if len(live) != 5 || live[0].Query != command || live[0].Frame != nil || live[0].Parent != live[1].Node {
						t.Errorf("computed SQL lost borrowed query owner at %s: %#v", event, live)
						return
					}
					if queryNode == nil {
						queryNode = live[0].Node
					}
					if live[0].Node != queryNode {
						t.Error("query owner changed during execution/cleanup", event)
					}
					live = live[1:]
				}
				for _, entry := range live {
					if entry.Query != "" {
						t.Error("query owner escaped actual computed SQL execution", event)
					}
				}
				if len(live) != 4 || live[0].Definition != leaf.SQL || live[0].Frame == nil || live[1].SQLName != "diagnostics_live_sql" || live[1].Statement != 2 || live[2].Definition != outer.SQL || live[2].Frame == nil || live[3].Node != beforeLive[0].Node {
					t.Errorf("actual PL/expression/SQL invocation lost live ancestors at %s: %#v", event, live)
					return
				}
				if activeFrame == nil {
					activeFrame = live[0].Frame
				}
				if live[0].Frame != activeFrame || live[0].Frame == live[2].Frame || live[0].Origin.Action != site.action || live[2].Origin.Action != "at RETURN" {
					t.Error("wrong live frame/action owners", event, live)
				}
				sites := core.ScopeDiagnosticsExpressionSites(s)
				if len(sites) < site.minSites || sites[len(sites)-1] != baselineSite {
					t.Error("expression activation lost enclosing operation-private sites", event, len(sites))
				}
				text := core.ScopeDiagnosticsContext(s)
				if !strings.Contains(text, `SQL function "diagnostics_live_sql" statement 2`) || strings.Count(text, "PL/pgSQL function ") != 2 || !strings.Contains(text, "at RETURN") {
					t.Error("live context omitted actual suspended owner", event, text)
				}
				if executing && !strings.HasPrefix(text, "SQL statement \""+command+"\"\n") {
					t.Error("live query renderer lost exact command spelling", event, text)
				}
				if retainedText == "" {
					retainedText = text
				}
				lexical := activeFrame
				if site.name == "dynamic_execute" || site.name == "dynamic_return_query" {
					// Computed SQL uses only closed USING parameters, never
					// the suspended PL frame's lexical variable bindings.
					lexical = nil
				}
				if core.ScopeAuthority(s) == authority || core.ScopeAuthority(s) == nil || core.ForeignTableScopeOwner(core.ScopeForeignTablesOrZero(s)) == outerOwner || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != lexical {
					t.Error("callback escaped lexical/authority/resource activation", event)
				}
			}
			provider.observe, scan.observe = observe, observe
			provider.tx.check = unlocked
			switch outcome {
			case "sql_error", "primary_close_fault":
				scan.primary = primary
			case "ordinary_error":
				scan.primary = ordinary
			case "cancel":
				scan.cancel = cancel
			}
			scan.onEnd = func(ctx context.Context) error {
				observe("end")
				if ctx.Err() != nil {
					t.Error("producer cleanup inherited cancellation")
				}
				if outcome == "close_fault" || outcome == "primary_close_fault" {
					return secondary
				}
				return nil
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			value, callErr := core.ScopeInvokePLRoutine(s, outer, input, read, sqltype.CollationRef{})
			switch outcome {
			case "success":
				if callErr != nil {
					t.Fatal(callErr)
				}
				if n, err := value.Integer(); err != nil || n != 7 {
					t.Fatal("real result", n, err)
				}
			case "sql_error":
				if !errors.Is(callErr, primary) {
					t.Fatal("lost primary SQL identity", callErr)
				}
			case "ordinary_error":
				if !errors.Is(callErr, ordinary) {
					t.Fatal("lost ordinary cause", callErr)
				}
			case "cancel":
				if !errors.Is(callErr, context.Canceled) {
					t.Fatal("lost actual cancellation", callErr)
				}
			case "close_fault", "primary_close_fault":
				var fatal *proc.PLExceptionCleanupError
				if !errors.As(callErr, &fatal) || !errors.Is(fatal.Cleanup, secondary) || !errors.Is(callErr, secondary) || outcome == "primary_close_fault" && (!errors.Is(callErr, primary) || !errors.Is(fatal.Primary, primary)) {
					t.Fatal("lost fatal producer cleanup/primary identity", callErr)
				}
			}
			if scan.ends != 1 || scan.rescans != 0 || scan.cleanupCanceled || len(events) < 3 || events[0] != "begin" || events[len(events)-1] != "end" {
				t.Fatal("producer not closed exactly once", events, scan.ends)
			}
			if !reflect.DeepEqual(core.ScopeDiagnosticsLive(s), beforeLive) || !reflect.DeepEqual(core.ScopeDiagnosticsExpressionSites(s), beforeSites) || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), beforeDemand) || core.ScopeAuthority(s) != authority || authority.Host == nil || core.ForeignTableScopeOwner(core.ScopeForeignTablesOrZero(s)) != outerOwner || s.Expected != beforeExpected || s.Identity != beforeIdentity || !s.Completed || core.ScopeSessionCompleted(s) != beforeSessionCompleted || core.ScopeActiveSQLRoutineDepth(s) != beforeDepth || core.ScopeEffectCommand(s) != 93 {
				t.Fatal("invocation failed exact outer live/scratch/depth/identity restoration")
			}
			diagnosticsLiveIdentity(t, core.ScopeDiagnosticsLive(s), beforeLive, core.ScopeDiagnosticsExpressionSites(s), beforeSites)
			if retainedText == "" || !strings.Contains(retainedText, "at RETURN") || activeFrame == nil || activeFrame.Values != nil || activeFrame.HandledError() != nil || activeFrame.RowCount() != 0 {
				t.Fatal("owned text/frame cleanup after live pop")
			}
			if outer.SQL.PL.ValidateDefinition(outer.SQL) != nil || leaf.SQL.PL.ValidateDefinition(leaf.SQL) != nil {
				t.Fatal("invocation mutated definitions")
			}
			if outcome == "success" && site.name == "initializer" {
				var recursive *catalog.Procedure
				for candidate := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "diagnostics_reentry") {
					recursive = candidate
				}
				if recursive == nil {
					t.Fatal("missing recursive declaration")
				}
				diagnosticsExpressionReentry(t, s, read, recursive, provider, scan, beforeLive, beforeSites)
			}
		})
	}
	}
}

// Reentry is a property of a currently active expression occurrence, not a bit
// cached on a shared program. Real callback frames distinguish all three calls.
func diagnosticsExpressionReentry(t *testing.T, s *core.StatementScope, read catalog.Read, declaration *catalog.Procedure, provider *diagnosticsLiveProvider, scan *diagnosticsLiveScan, enclosing []core.DiagnosticsLiveEntry, enclosingSites []*parser.Select) {
	t.Helper()
	program := declaration.SQL.PL
	body := program.Root.Statements[0].Block
	for round := range 2 {
		var seen []int64
		var frames []*proc.PLFrame
		var texts []sqltype.Value
		observe := func(event string) {
			if event == "begin" {
				scan.position = 0
			}
			if event != "begin" {
				return
			}
			live := core.ScopeDiagnosticsLive(s)
			if len(live) < 3 || live[0].Query != "SELECT 1 FROM diagnostics_live_target" || live[0].Frame != nil || live[0].Parent != live[1].Node {
				t.Error("recursive PERFORM missing actual SQL query owner", live)
				return
			}
			live = live[1:]
			if len(live) < 2 || live[0].Definition != declaration.SQL || live[0].Frame == nil {
				t.Error("recursive callback missing actual owner")
				return
			}
			f := live[0].Frame
			n, err := f.Values[0].Integer()
			if err != nil {
				t.Error(err)
				return
			}
			want := int64(3)
			if n == 1 {
				want = 1
			}
			c, err := f.Values[body.Bindings["c"]].Integer()
			if err != nil || c != want || f.RowCount() != want {
				t.Error("active-site count classification", round, n, c, f.RowCount(), err)
			}
			oid, oidErr := f.Values[body.Bindings["o"]].OID()
			if oidErr != nil || oid != declaration.RoutineOID() || oid == 0 {
				t.Error("diagnostics fabricated routine identity", oid, oidErr)
			}
			stack := f.Values[body.Bindings["stack"]]
			if text, err := stack.Text(); err != nil || !strings.Contains(text, "at GET DIAGNOSTICS") {
				t.Error("actual GET context ownership", text, err)
			}
			texts = append(texts, stack)
			seen = append(seen, n)
			frames = append(frames, f)
			sites := core.ScopeDiagnosticsExpressionSites(s)
			occurrences := 0
			site := body.Statements[1].If.Arms[0].Statements[0].Query
			for _, active := range sites {
				if active == site {
					occurrences++
				}
			}
			if occurrences != 2-int(n) {
				t.Error("active same-site occurrence ownership", n, occurrences)
			}
		}
		provider.observe, scan.observe = observe, observe
		scan.onEnd = func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Error("reentry cleanup canceled")
			}
			return nil
		}
		input := proc.NewFrame(declaration.SQL.Inputs)
		argument, err := sqltype.Integer(declaration.SQL.Inputs[0].Type, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := input.Bind(0, argument, declaration.SQL.Inputs[0]); err != nil {
			t.Fatal(err)
		}
		value, err := core.ScopeInvokePLRoutine(s, declaration, input, read, sqltype.CollationRef{})
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := value.Integer(); err != nil || n != 3 {
			t.Fatal("outer simple expression did not preserve count", n, err)
		}
		if !reflect.DeepEqual(seen, []int64{0, 1, 2}) || len(frames) != 3 || frames[0] == frames[1] || frames[1] == frames[2] {
			t.Fatal("missing distinct recursive callback owners", seen)
		}
		for _, f := range frames {
			if f.Values != nil || f.RowCount() != 0 || f.HandledError() != nil {
				t.Fatal("completed recursive frame retained state")
			}
		}
		for _, text := range texts {
			if owned, err := text.Text(); err != nil || !strings.Contains(owned, "at GET DIAGNOSTICS") {
				t.Fatal("returned context datum borrowed popped frame", owned, err)
			}
		}
		if declaration.SQL.PL != program || program.ValidateDefinition(declaration.SQL) != nil || !reflect.DeepEqual(core.ScopeDiagnosticsLive(s), enclosing) || !reflect.DeepEqual(core.ScopeDiagnosticsExpressionSites(s), enclosingSites) {
			t.Fatal("recursive operation contaminated shared program/enclosing active sites")
		}
		diagnosticsLiveIdentity(t, core.ScopeDiagnosticsLive(s), enclosing, core.ScopeDiagnosticsExpressionSites(s), enclosingSites)
	}
}

func diagnosticsLiveIdentity(t *testing.T, current, previous []core.DiagnosticsLiveEntry, sites, previousSites []*parser.Select) {
	t.Helper()
	if len(current) != len(previous) || len(sites) != len(previousSites) {
		t.Fatal("restoration changed live owner depth")
	}
	for i := range previous {
		if current[i].Node != previous[i].Node || current[i].Parent != previous[i].Parent || current[i].Frame != previous[i].Frame || current[i].Definition != previous[i].Definition {
			t.Fatal("restoration substituted a live owner", i)
		}
	}
	for i := range previousSites {
		if sites[i] != previousSites[i] {
			t.Fatal("restoration substituted expression site", i)
		}
	}
}

func TestPLpgSQLDiagnosticsCursorCleanupOwnership(t *testing.T) {
	for _, withPrimary := range []bool{false, true} {
		name := "close_failure"
		if withPrimary {
			name = "primary_and_close_failure"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			primary := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
			secondary := errors.New("diagnostics cursor End failure")
			scan := &diagnosticsLiveScan{}
			provider := &diagnosticsLiveProvider{scan: scan, tx: &plExceptionFaultTx{}, observe: func(string) {}}
			scan.observe = func(string) {}
			if withPrimary {
				scan.primary = primary
			}
			wrapper, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "diagnostics_cursor_handler"}, Wrapper: provider})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{wrapper}, nil,
				func(_ context.Context, v driver.Value, u sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
					if v == nil {
						return sqltype.Null(u.Type), nil
					}
					return sqltype.Integer(u.Type, v.(int64))
				},
				func(_ context.Context, v sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
					if v.IsNull() {
						return nil, nil
					}
					return v.Integer()
				},
			); err != nil {
				t.Fatal(err)
			}
			owner := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE FOREIGN DATA WRAPPER diagnostics_cursor HANDLER diagnostics_cursor_handler",
				"CREATE SERVER diagnostics_cursor_server FOREIGN DATA WRAPPER diagnostics_cursor",
				"CREATE USER MAPPING FOR CURRENT_USER SERVER diagnostics_cursor_server",
				"CREATE FOREIGN TABLE diagnostics_cursor_target(v integer) SERVER diagnostics_cursor_server",
				`CREATE FUNCTION pg_catalog.diagnostics_cursor_failure() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; cur CURSOR FOR SELECT 7 FROM diagnostics_cursor_target; BEGIN PERFORM 1 UNION ALL SELECT 2 UNION ALL SELECT 3; OPEN cur; FETCH ABSOLUTE 2 FROM cur INTO x; CLOSE cur; RETURN x; END$$`,
			} {
				enginetest.SessionSQL(t, owner, query)
			}
			enginetest.TransactionBegin(t, owner)
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			s, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Finish(context.Canceled)
			// Admit a real enclosing statement before borrowing its catalog and
			// execution authority for the direct routine invocation below.
			ast := enginetest.Statement(t, "SELECT 1")
			description, err := s.Describe(ast)
			if err != nil {
				t.Fatal(err)
			}
			s.Expected = &description
			result, err := s.Execute(ast, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, len(result.Rows.Schema()))
			for {
				ok, err := result.Rows.Next(op.Context(), row)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
			}
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			read := e.CatalogRead()
			var routine *catalog.Procedure
			for candidate := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "diagnostics_cursor_failure") {
				routine = candidate
			}
			if routine == nil {
				t.Fatal("missing cursor routine")
			}
			beforeLive, beforeSites := core.ScopeDiagnosticsLive(s), core.ScopeDiagnosticsExpressionSites(s)
			beforeDemand, beforeExpected := core.ScopeSQLRoutine(s), s.Expected
			beforeAuthority, beforeIdentity := core.ScopeAuthority(s), s.Identity
			beforeDepth := core.ScopeActiveSQLRoutineDepth(s)
			outerOwner := &exec.ForeignScanOwner{}
			core.SetForeignTableScopeOwner(core.ScopeForeignTables(s), outerOwner)
			var activeFrame *proc.PLFrame
			observedEnd := false
			scan.onEnd = func(ctx context.Context) error {
				observedEnd = true
				if ctx.Err() != nil {
					t.Error("cursor cleanup inherited cancellation")
				}
				live := core.ScopeDiagnosticsLive(s)
				if len(live) != 1 || live[0].Frame == nil || live[0].Origin.Action != "at FETCH" {
					t.Errorf("cleanup not owned by actual FETCH: %#v", live)
				} else {
					activeFrame = live[0].Frame
					flag, flagErr := activeFrame.Values[activeFrame.Program.FoundSlot].Boolean()
					if activeFrame.RowCount() != 3 || flagErr != nil || !flag {
						t.Error("failed cursor cleanup changed prior status", activeFrame.RowCount(), flag, flagErr)
					}
				}
				return secondary
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			_, callErr := core.ScopeInvokePLRoutine(s, routine, input, read, sqltype.CollationRef{})
			var fatal *proc.PLExceptionCleanupError
			if !errors.As(callErr, &fatal) || !errors.Is(callErr, secondary) || !errors.Is(fatal.Cleanup, secondary) || withPrimary && (!errors.Is(callErr, primary) || !errors.Is(fatal.Primary, primary)) {
				t.Fatal("lost cursor cleanup/primary identity", callErr)
			}
			// Cursor exhaustion/failed motion owns this End before destination
			// assignment, not a synthetic failure of detached FinalRows.Close.
			if !observedEnd || scan.ends != 1 || scan.rescans != 0 || scan.cleanupCanceled {
				t.Fatal("cursor producer not ended exactly once", observedEnd, scan.ends)
			}
			if !reflect.DeepEqual(core.ScopeDiagnosticsLive(s), beforeLive) || !reflect.DeepEqual(core.ScopeDiagnosticsExpressionSites(s), beforeSites) || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), beforeDemand) || core.ScopeAuthority(s) != beforeAuthority || s.Identity != beforeIdentity || s.Expected != beforeExpected || core.ScopeActiveSQLRoutineDepth(s) != beforeDepth || core.ForeignTableScopeOwner(core.ScopeForeignTablesOrZero(s)) != outerOwner {
				t.Fatal("cursor failure lost enclosing lexical/live ownership")
			}
			if activeFrame == nil || activeFrame.Values != nil || activeFrame.HandledError() != nil || activeFrame.RowCount() != 0 {
				t.Fatal("failed cursor frame retained state")
			}
		})
	}
}
