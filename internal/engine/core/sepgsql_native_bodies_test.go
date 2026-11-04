package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSepgsqlSimulationNativeBodies(t *testing.T) {
	t.Run("generated_earth_check_actual_c_leaf", func(t *testing.T) {
		e := engine.New()
		for _, query := range []string{"CREATE EXTENSION cube", "CREATE EXTENSION earthdistance", "CREATE TABLE native_earth_check(v earth)"} {
			enginetest.Run(t, e, query)
		}
		runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Policy: sepgsql.Policy{
			ID: "earth-check-source", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U", "D"}, InitialContext: "A", UnlabeledContext: "P",
			Rules: []sepgsql.Rule{
				{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
				{Source: "R", Target: "P", Class: "*", Allow: []string{"*"}},
				{Source: "R", Target: "S", Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
				{Source: "R", Target: "U", Class: "db_procedure", Allow: []string{"execute"}},
				{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
				{Source: "T", Target: "*", Class: "*", Allow: []string{"*"}},
			},
			TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		err = e.InstallSepgsqlSimulation(runtime, true)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SET client_min_messages=log")
		for _, helper := range []string{"cube_is_point(cube)", "cube_dim(cube)", "cube_distance(cube,cube)"} {
			label := func(context string) {
				t.Helper()
				enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
				enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON FUNCTION "+helper+" IS '"+context+"'")
				enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			}
			entries := func(start int, context string) int {
				count := 0
				for _, notice := range op.Notices[start:] {
					if notice.Severity == "LOG" && strings.Contains(notice.Message, "scontext=R tcontext="+context+" tclass=db_procedure") && strings.Contains(notice.Message, "entrypoint") {
						count++
					}
				}
				return count
			}
			label("S")
			start := len(op.Notices)
			enginetest.RoleExecute(t, e, op, "INSERT INTO native_earth_check VALUES('(6378168)'::earth)")
			if entries(start, "S") != 1 {
				t.Fatal("selected CHECK helper did not enter exactly once", helper, op.Notices[start:])
			}
			sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
			start = len(op.Notices)
			enginetest.RoleExecute(t, e, op, "INSERT INTO native_earth_check VALUES(NULL::earth)")
			if entries(start, "S") != 0 {
				t.Fatal("NULL CHECK entered a C helper", helper, op.Notices[start:])
			}
			for _, context := range []string{"U", "D"} {
				label(context)
				start = len(op.Notices)
				_, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO native_earth_check VALUES('(6378168)'::earth)"), 0, nil)
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
				if context == "D" && entries(start, "D") != 0 {
					t.Fatal("execute-denied CHECK reached actual body", helper, op.Notices[start:])
				}
				sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
			}
			label("P")
		}
	})
	t.Run("earth_actual_nested_subject_and_expanded_null", func(t *testing.T) {
		e := engine.New()
		for _, q := range []string{"CREATE EXTENSION cube", "CREATE EXTENSION earthdistance"} {
			enginetest.Run(t, e, q)
		}
		var mu sync.Mutex
		var events []sepgsql.AuditEvent
		runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Audit: func(event sepgsql.AuditEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }, Policy: sepgsql.Policy{
			ID: "native-body-owner", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
			DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
			Rules: []sepgsql.Rule{
				{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
				{Source: "R", Target: "P", Class: "*", Allow: []string{"*"}},
				{Source: "R", Target: "S", Class: "db_procedure", Allow: []string{"execute", "entrypoint"}},
				{Source: "R", Target: "*", Class: "process", Allow: []string{"setcurrent", "dyntransition", "transition"}},
				{Source: "T", Target: "*", Class: "*", Allow: []string{"*"}},
			}, TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		err = e.InstallSepgsqlSimulation(runtime, true)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			"CREATE TABLE native_earth(id integer,e earth,d double precision)",
			"INSERT INTO native_earth VALUES(1,ll_to_earth(0,0),0),(2,NULL,0)",
			"SECURITY LABEL ON FUNCTION gc_to_sec(double precision) IS 'S'",
			"SECURITY LABEL ON FUNCTION earth() IS 'U'",
			"SECURITY LABEL ON FUNCTION cube_enlarge(cube,double precision,integer) IS 'S'",
		} {
			enginetest.Run(t, e, q)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		finished := false
		defer func() {
			if !finished {
				done()
			}
		}()
		mu.Lock()
		events = nil
		mu.Unlock()
		enginetest.RoleExecute(t, e, op, "SET client_min_messages=log")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		func() {
			result := enginetest.RoleExecute(t, e, op, "SELECT cube_ll_coord(earth_box(e,d),1) FROM native_earth WHERE id=1")
			defer result.Rows.Close()
			row := make([]sqltype.Value, 1)
			present, err := result.Rows.Next(t.Context(), row)
			if err != nil || !present {
				t.Fatal("missing genuine Earth coordinate", present, err)
			}
			coordinate, err := row[0].Float()
			if err != nil || coordinate != 6378168 {
				t.Fatal("genuine Earth coordinate", coordinate, err)
			}
			if present, err := result.Rows.Next(t.Context(), row); err != nil || present {
				t.Fatal("unexpected Earth coordinate tail", present, err)
			}
		}()
		// R cannot initialize earth(). Success above requires the actual gc_to_sec
		// invocation to enter T and initialize its own genuine body before its math.
		observed := slices.Clone(op.Notices)
		found := false
		for _, event := range observed {
			if event.Severity == "LOG" && event.Code == "00000" && strings.Contains(event.Message, "scontext=T tcontext=U tclass=db_procedure") && strings.Contains(event.Message, "execute") {
				found = true
			}
		}
		if !found {
			t.Fatal("nested conversion did not initialize its actual entered body", observed)
		}
		sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
		enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON FUNCTION earth_box(earth,double precision) IS 'S'")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		first := len(op.Notices)
		sepgsqlPhaseRows(t, e, op, "SELECT earth_box(e,d) FROM native_earth WHERE id=2", [][]driver.Value{{nil}})
		observed = slices.Clone(op.Notices[first:])
		conversion, outer, enlarge := 0, 0, 0
		for _, event := range observed {
			if event.Severity != "LOG" || event.Code != "00000" || !strings.HasPrefix(event.Message, "SELinux: allowed { entrypoint } scontext=R tcontext=S tclass=db_procedure ") {
				continue
			}
			switch {
			case strings.HasSuffix(event.Message, `name="\"\".public.gc_to_sec"`):
				conversion++
			case strings.HasSuffix(event.Message, `name="\"\".public.earth_box"`):
				outer++
			case strings.HasSuffix(event.Message, `name="\"\".public.cube_enlarge"`):
				enlarge++
			}
		}
		if conversion != 1 || outer != 0 || enlarge != 0 {
			t.Fatal("expanded NULL must consume only the demanded conversion kernel", conversion, outer, enlarge, observed)
		}
		sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		// The host observer is deliberately deferred until the genuine operation
		// releases its lease. Preserve delivery coverage without pretending it
		// is an in-flight kernel observer.
		mu.Lock()
		premature := len(events)
		mu.Unlock()
		if premature != 0 {
			t.Fatal("host audit dispatched before Operation.Done", premature)
		}
		done()
		finished = true
		mu.Lock()
		delivered := slices.Clone(events)
		mu.Unlock()
		messages := make(map[string]int)
		for _, event := range delivered {
			messages[event.Message]++
		}
		for _, notice := range op.Notices {
			if notice.Severity != "LOG" || !strings.HasPrefix(notice.Message, "SELinux:") {
				continue
			}
			if messages[notice.Message] == 0 {
				t.Fatal("actual in-flight audit missing after Operation.Done", notice, delivered)
			}
			messages[notice.Message]--
		}
	})
	t.Run("heapattrs_actual_child_superuser_and_strict_order", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION pageinspect")
		enginetest.Run(t, e, "CREATE ROLE native_heap_reader")
		enginetest.Run(t, e, "GRANT USAGE ON SCHEMA public TO native_heap_reader")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION native_heap_reader")
		// NONSTRICT SQL wrappers initialize, but the actual STRICT heap child is
		// not invoked. A raw-superuser check on either outer wrapper is wrong.
		sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM heap_page_item_attrs(NULL::bytea,NULL::regclass)", [][]driver.Value{{int64(0)}})
		query := "SELECT count(*) FROM heap_page_item_attrs(''::bytea,NULL::regclass)"
		_, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "must be superuser to use raw page functions", Context: "SQL function \"heap_page_item_attrs\" statement 1\nSQL function \"heap_page_item_attrs\" statement 1"})
		query = "SELECT tuple_data_split(4294967295::oid,NULL::bytea,1,99,'bad')"
		_, err = e.Execute(op, enginetest.Statement(t, query), 0, nil)
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "must be superuser to use raw page functions"})
	})
	t.Run("freespace_actual_source_children", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION pg_freespacemap")
		enginetest.Run(t, e, "CREATE TABLE native_free(v integer)")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		for _, q := range []string{
			"SELECT count(*) FROM pg_freespace(NULL::regclass)",
			"SELECT count(*) FROM pg_freespace(4294967295::oid::regclass)",
			"SELECT count(*) FROM pg_freespace('native_free'::regclass)",
		} {
			sepgsqlPhaseRows(t, e, op, q, [][]driver.Value{{int64(0)}})
		}
		// A real entered SQL body must consume the selected C2 execute grant even
		// when a NULL relation makes its series empty. This is not per-row preflight.
		enginetest.Run(t, e, "CREATE ROLE native_free_reader")
		enginetest.Run(t, e, "GRANT USAGE ON SCHEMA public TO native_free_reader")
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION native_free_reader")
		enginetest.RoleExecute(t, e, op, "SET plan_cache_mode=force_generic_plan")
		invoke := func() error {
			_, err := e.Execute(op, enginetest.Statement(t, "SELECT count(*) FROM pg_freespace($1::regclass)"), 1, func(_ context.Context, signature analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
				frame[0] = sqltype.Null(signature.Types[0].Type)
				return nil
			})
			return err
		}
		// Released installation revokes PUBLIC EXECUTE on both overloads. An
		// outer denial has no body frame; grant only that wrapper to reach C2.
		enginetest.Diagnostic(t, invoke(), sqlstate.Error{Code: "42501", Message: "permission denied for function pg_freespace"})
		enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION pg_freespace(regclass) TO native_free_reader")
		enginetest.Diagnostic(t, invoke(), sqlstate.Error{Code: "42501", Message: "permission denied for function pg_freespace", Context: "SQL function \"pg_freespace\" statement 1"})
	})
}

func TestSepgsqlSimulationSurgeryScalar(t *testing.T) {
	for _, method := range []string{"heap_force_kill", "heap_force_freeze"} {
		for _, label := range []string{"S", "U"} {
			t.Run(method+"/"+label, func(t *testing.T) {
				e := engine.New()
				runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, AuditAllowed: true, Policy: sepgsql.Policy{
					ID: "surgery-scalar-owner", Version: "1", Contexts: []string{"A", "R", "T", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "P",
					Rules: []sepgsql.Rule{
						{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
						{Source: "R", Target: "*", Class: "*", Allow: []string{"*"}},
						{Source: "T", Target: "*", Class: "*", Allow: []string{"*"}},
						{Source: "R", Target: "U", Class: "db_procedure", Deny: []string{"entrypoint"}},
						{Source: "R", Target: "P", Class: "db_table", Deny: []string{"update", "delete"}},
						{Source: "T", Target: "P", Class: "db_table", Deny: []string{"update", "delete"}},
					}, TrustedTransitions: []sepgsql.TrustedTransition{{Source: "R", Procedure: "S", Result: "T"}, {Source: "R", Procedure: "U", Result: "T"}},
				}})
				if err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				err = e.InstallSepgsqlSimulation(runtime, true)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				for _, query := range []string{
					"CREATE EXTENSION pg_surgery",
					"CREATE TABLE incoming_heap(id integer)",
					"INSERT INTO incoming_heap VALUES(1),(2)",
					"SECURITY LABEL ON FUNCTION " + method + "(regclass,tid[]) IS '" + label + "'",
				} {
					enginetest.Run(t, e, query)
				}
				op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT "+method+"('incoming_heap'::regclass,$1::tid[])", "SELECT sepgsql_setcon('R')")
				op.NoticeThreshold = session.LevelLog
				node := project.Expressions[0].(plan.InstalledPgsurgeryExpr)
				if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
					t.Fatal(err)
				}
				arrayType := node.Args[1].ResultUsage().Type
				array, err := arrayType.InputWithEnvironment(evaluation.Context, "{}", -1, evaluation.FunctionInput())
				if err != nil {
					t.Fatal(err)
				}
				evaluation.Parameters = []sqltype.Value{array}
				entries := func() int {
					n := 0
					for _, event := range op.Notices {
						if strings.Contains(event.Message, "tcontext="+label+" tclass=db_procedure") && strings.Contains(event.Message, "entrypoint") {
							n++
						}
					}
					return n
				}
				// The real row/parameter-derived NULL path retains executor
				// initialization but never reaches fmgr/kernel entry.
				evaluation.Parameters[0] = sqltype.Null(arrayType)
				before := entries()
				value, err := node.Eval(evaluation)
				if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type || entries() != before || op.Session.Sepgsql.Current() != "R" {
					t.Fatal("STRICT NULL entered scalar or lost subject", value, err, op.Notices)
				}
				evaluation.Parameters[0] = array
				sibling, err := core.HostOf(scope).NewRoutineInitialization()
				if err != nil {
					t.Fatal(err)
				}
				for _, owner := range []exec.RoutineInitialization{{}, sibling} {
					lost := *evaluation
					lost.RoutineInitialization = owner
					if _, err := node.Eval(&lost); err == nil || entries() != before || op.Session.Sepgsql.Current() != "R" {
						t.Fatal("scalar adopted missing or sibling initializer", err)
					}
				}
				lost := *evaluation
				lost.Catalog = catalog.Read{}
				if _, err := node.Eval(&lost); err == nil {
					t.Fatal("scalar adopted ambient catalog")
				}
				canceled, cancel := context.WithCancel(evaluation.Context)
				cancel()
				lost = *evaluation
				lost.Context = canceled
				if _, err := node.Eval(&lost); !errors.Is(err, context.Canceled) || entries() != before {
					t.Fatal("canceled scalar entered kernel", err)
				}
				// Evaluate both actual inputs in formal order, before entry.
				probed := node
				probed.Args = append([]expr.Expr(nil), node.Args...)
				calls := 0
				for slot, child := range node.Args {
					i, child := slot, child
					probed.Args[i] = enginetest.QueryProbe(child.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						if op.Session.Sepgsql.Current() != "R" || calls != i {
							t.Fatal("input inherited scalar subject or reordered", calls, i)
						}
						calls++
						return child.Eval(c)
					})
				}
				value, err = probed.Eval(evaluation)
				if calls != 2 || entries() != before+1 || op.Session.Sepgsql.Current() != "R" {
					t.Fatal("actual scalar boundary did not surround kernel only", calls, err, op.Notices)
				}
				if label == "U" {
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
				} else if err != nil || value.IsNull() {
					t.Fatal("allowed real empty-array kernel rejected", value, err)
				}
				invalid, err := arrayType.InputWithEnvironment(evaluation.Context, "{NULL}", -1, evaluation.FunctionInput())
				if err != nil {
					t.Fatal(err)
				}
				evaluation.Parameters[0] = invalid
				_, err = node.Eval(evaluation)
				if label == "U" {
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
				} else {
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "22004", Message: "array must not contain nulls"})
				}
				if op.Session.Sepgsql.Current() != "R" {
					t.Fatal("kernel error leaked trusted subject")
				}
				failure := errors.New("actual surgery argument failure")
				probed = node
				probed.Args = append([]expr.Expr(nil), node.Args...)
				probed.Args[0] = enginetest.QueryProbe(node.Args[0].ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
					return sqltype.Null(node.Args[0].ResultUsage().Type), nil
				})
				probed.Args[1] = enginetest.QueryProbe(node.Args[1].ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
					return sqltype.Value{}, failure
				})
				before = entries()
				if _, err := probed.Eval(evaluation); !errors.Is(err, failure) || entries() != before {
					t.Fatal("STRICT skipped later input error or entered kernel", err)
				}
				scope.Finish(nil)
				if _, err := node.Eval(evaluation); err == nil {
					t.Fatal("closed owner retained scalar entry")
				}
				if label == "S" {
					// These source TIDs are genuine, not assumed logical offsets.
					// The successful effects require no invented table UPDATE gate.
					func() {
						result := enginetest.RoleExecute(t, e, op, "SELECT "+method+"('incoming_heap'::regclass,ARRAY[ctid]) FROM incoming_heap WHERE id=1")
						defer result.Rows.Close()
						columns := result.Rows.Schema()
						if len(columns) != 1 || columns[0].Usage.Type == nil || columns[0].Usage.Type.ID() != sqltype.Void {
							t.Fatal("actual surgery result lost VOID schema", columns)
						}
						var values [1]sqltype.Value
						if present, err := result.Rows.Next(op.Context(), values[:]); err != nil || !present {
							t.Fatal("actual source TID surgery result", present, err)
						}
						if values[0].IsNull() || values[0].Type() != columns[0].Usage.Type {
							t.Fatal("successful surgery did not return non-NULL VOID", values[0])
						}
						text, err := values[0].Type().Output(op.Context(), values[0])
						if err != nil || text != "" {
							t.Fatal("successful surgery VOID presentation", text, err)
						}
						if present, err := result.Rows.Next(op.Context(), values[:]); err != nil || present {
							t.Fatal("actual source TID surgery returned extra rows", present, err)
						}
						if err := result.Rows.Close(); err != nil {
							t.Fatal(err)
						}
					}()
					if method == "heap_force_kill" {
						sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM incoming_heap", [][]driver.Value{{int64(1)}})
					} else {
						sepgsqlPhaseRows(t, e, op, "SELECT xmin::text FROM incoming_heap WHERE id=1", [][]driver.Value{{"2"}})
					}
				}
			})
		}
	}
}
