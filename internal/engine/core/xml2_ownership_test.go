package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Retain the real prepared definition, accepted folds and portal graphs. Public
// SQL values and live ACL/mode/control witnesses live in reference/extension_xml2.
func TestXML2PreparedOwnership(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	err := enginetest.ExtensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "xml2"}})
	done()
	if err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const query = "SELECT xml_valid('<r/>'),xpath_string('<r>owned</r>','.'),xpath_number('<r>16777217</r>','.'),xpath_bool('<r/>','.'),xpath_nodeset('<r><a>owned</a></r>','/r/a','o','i'),xpath_nodeset('<r><a>owned</a></r>','/r/a','i'),xpath_nodeset('<r><a>owned</a></r>','/r/a'),xpath_list('<r><a>owned</a></r>','/r/a','|'),xpath_list('<r><a>owned</a></r>','/r/a')"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("xml2_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("xml2_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual definition")
	}
	analysis, owner := definition.Description.Analysis, definition.Description.Folds
	body, source := definition.Body, definition.Source
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
	if definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.PlanOwner != s.SessionToken || analysis.Domain != view.Domain() || analysis.Epoch != view.Epoch() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("prepare owner/generation or premature folding")
	}
	var procedures [9]*catalog.Procedure
	// This query covers all scalar/set signatures; xpath_table retains its
	// separate real-definition/publication ownership proof and sealed slot.
	for slot, member := range [...]int{0, 1, 2, 3, 5, 6, 7, 8, 9} {
		a := ext.Members[member]
		p := view.MustProcedure(a)
		procedures[slot] = p
		found := false
		for _, d := range analysis.Dependencies {
			if d.Ref.Address == a && d.Ref.Domain() == view.Domain() && d.Ref.Epoch == view.Epoch() && d.Revision == p.Revision() {
				found = true
			}
		}
		if !found || analysis.FoldCallables[a] != p || p.Origin.Installation != ext.Installation {
			t.Fatal("lost exact callable backing", slot)
		}
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "xml2_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing accepted binding")
		}
		return b
	}
	first := bind("xml2_first")
	firstResource, firstRoot, firstHolder := first.Resource, first.Resource.Root, first.Folds
	accepted := owner.Current()
	if len(accepted.Entries) != 8 || accepted.Generation == 0 {
		t.Fatal("expected eight actual immutable installed folds", accepted)
	}
	indices := [9]int{-1, -1, -1, -1, -1, -1, -1, -1, -1}
	for i, entry := range accepted.Entries {
		arity := 0
		for slot, p := range procedures {
			if entry.Callable == p {
				indices[slot] = i
				arity = [9]int{1, 2, 2, 2, 4, 3, 2, 3, 2}[slot]
			}
		}
		if entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || arity == 0 || len(entry.Inputs) != arity || entry.Value.Type() != entry.Result.Type {
			t.Fatal("lost fold provenance/input generation", entry)
		}
		for _, input := range entry.Inputs {
			if input.Usage.Type != analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}] || input.Value.Type() != input.Usage.Type || input.Value.IsNull() {
				t.Fatal("operand backing", input)
			}
		}
	}
	if indices[0] != -1 {
		t.Fatal("STABLE validator was folded", indices)
	}
	for slot := 1; slot < len(indices); slot++ {
		if indices[slot] < 0 {
			t.Fatal("missing genuine selected overload fold", slot, indices)
		}
	}
	second := bind("xml2_second")
	secondResource, secondRoot, secondHolder := second.Resource, second.Resource.Root, second.Folds
	if first == second || firstResource == secondResource || firstRoot == secondRoot || firstHolder == secondHolder || first.Birth == 0 || first.Birth == second.Birth {
		t.Fatal("shared mutable portal ownership")
	}
	firstFolds, secondFolds := *firstHolder, *secondHolder
	savedEntries := slices.Clone(accepted.Entries)
	savedDependencies := slices.Clone(analysis.Dependencies)
	savedCallables, savedTypes := maps.Clone(analysis.FoldCallables), maps.Clone(analysis.FoldTypes)
	assertFolds := func(g exec.AcceptedFolds) {
		t.Helper()
		if g.Generation != accepted.Generation || len(g.Entries) != 8 || &g.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(g.Dependencies, accepted.Dependencies) {
			t.Fatal("accepted fold generation/backing replaced")
		}
		for i, entry := range g.Entries {
			if !enginetest.SamePublishedFold(entry, savedEntries[i]) || &entry.Inputs[0] != &accepted.Entries[i].Inputs[0] || !sqltype.SameDatum(entry.Value, savedEntries[i].Value) {
				t.Fatal("immutable fold inputs/provenance/datum changed", i)
			}
		}
	}
	assertRoot := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil || len(root.Expressions) != 9 {
			t.Fatal("lost actual graph")
		}
		stable, ok := root.Expressions[0].(plan.InstalledXML2Expr)
		if !ok || stable.Procedure != procedures[0] || stable.RoutineReference != procedures[0] || stable.Ref.Address != ext.Members[0] || stable.Name != "xml_valid" || stable.IntrinsicVolatility() != expr.Stable || len(stable.Args) != 1 {
			t.Fatal("stable operation lost genuine recipe")
		}
		for slot := 1; slot < len(procedures); slot++ {
			literal, ok := root.Expressions[slot].(plan.LiteralExpr)
			entry := accepted.Entries[indices[slot]]
			if !ok || literal.RoutineReference != nil || literal.Source != entry.Source || literal.Occurrence != entry.Occurrence || literal.ResultUsage() != entry.Result || !sqltype.SameDatum(literal.Value, entry.Value) {
				t.Fatal("graph does not consume real installed fold", slot)
			}
		}
	}
	assertRoot(firstRoot)
	assertRoot(secondRoot)
	assertFolds(owner.Current())
	assertFolds(firstFolds)
	assertFolds(secondFolds)
	// Fail a genuine third producer, not a cloned or recomputed description.
	// Terminal detachment is distinct from mutation of accepted graph/fold data.
	terminal := bind("xml2_terminal")
	terminalResource, terminalRoot, terminalHolder := terminal.Resource, terminal.Resource.Root, terminal.Folds
	terminalFolds := *terminalHolder
	failure := errors.New("xml2 actual consumer failure")
	var executionError error
	command(func(scope *core.StatementScope) error {
		_, executionError = scope.ExecuteProtocolPortal("xml2_terminal", 0, func([]session.ProtocolField) error { return failure }, nil, nil, core.CopyProtocolHooks{})
		return nil
	})
	if !errors.Is(executionError, failure) || terminalResource.Root != nil {
		t.Fatal("failed producer did not detach its live graph", executionError)
	}
	assertRoot(terminalRoot)
	assertFolds(terminalFolds)
	assertFolds(*terminalHolder)
	assertFolds(owner.Current())
	assertRoot(firstRoot)
	assertRoot(secondRoot)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("xml2_terminal") })
	for _, cause := range []string{"cancellation", "retirement", "generation"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			operation, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Done()
			scope, err := e.StartStatement(operation)
			if err != nil {
				t.Fatal(err)
			}
			local := enginetest.CloneFoldDescription(t, definition.Description)
			scope.Expected = &local
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(operation)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			environment, err := e.PreparedEnvironmentLocked(auth, true)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
			if err != nil || publication == nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal("real publication", err)
			}
			publication.Group.Entries = slices.Clone(publication.Group.Entries)
			publication.Group.Private = true
			publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
			switch cause {
			case "cancellation":
				cancel()
			case "retirement":
				local.Retire()
			case "generation":
				local.Folds.Invalidate(true)
			}
			fenced := local.Folds.Current()
			err = publication.Accept(operation.Ctx, auth.Catalog, environment, view.Epoch())
			e.Mu.Unlock()
			_ = scope.Finish(err)
			if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
				t.Fatal("rejected publication accepted", err)
			}
			current := local.Folds.Current()
			if current.Generation != fenced.Generation || &current.Entries[0] != &accepted.Entries[0] {
				t.Fatal("rejected publication mutated backing")
			}
			if registry.Lookup("xml2_owned") != definition || definition.Body != body || definition.Source != source || definition.Description.Analysis != analysis || definition.Description.Folds != owner || !reflect.DeepEqual(analysis.Dependencies, savedDependencies) || !maps.Equal(analysis.FoldCallables, savedCallables) || !maps.Equal(analysis.FoldTypes, savedTypes) || first.Resource != firstResource || second.Resource != secondResource || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
				t.Fatal("rejected publication mutated accepted ownership")
			}
			assertFolds(owner.Current())
			assertFolds(*first.Folds)
			assertFolds(*second.Folds)
			assertRoot(firstRoot)
			assertRoot(secondRoot)
		})
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("xml2_owned") })
	if owner.Live() || owner.Current().Generation <= accepted.Generation || first.Retired || second.Retired {
		t.Fatal("definition retirement changed portal lifetime")
	}
	assertFolds(*first.Folds)
	assertFolds(*second.Folds)
	for _, name := range []string{"xml2_first", "xml2_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !first.Retired || !second.Retired || first.Folds != nil || second.Folds != nil || firstResource.Root != nil || secondResource.Root != nil {
		t.Fatal("portal resources leaked")
	}
	assertFolds(accepted)
	assertFolds(firstFolds)
	assertFolds(secondFolds)
	assertFolds(*firstHolder)
	assertFolds(*secondHolder)
	assertRoot(firstRoot)
	assertRoot(secondRoot)
	operation, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(operation, cycle)
	operation.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

// Exercise the real configuration-only host bridge with installed descriptors.
// Public fixtures separately invoke the kernel and observe notice suppression.
func TestXML2RoutineConfiguration(t *testing.T) {
	for _, wrapper := range []struct {
		slot int
		signature string
	}{
		{6, "xpath_nodeset(text,text,text)"},
		{7, "xpath_nodeset(text,text)"},
		{9, "xpath_list(text,text)"},
	} {
		for _, outcome := range []string{"success", "error", "cancel", "partial_entry"} {
			t.Run(wrapper.signature+"/"+outcome, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE EXTENSION xml2")
				view := e.CatalogRead()
				header := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
				original := view.MustProcedure(header.Members[wrapper.slot])
				if outcome == "success" {
					admission, finish := enginetest.RoleOperation(t, e, "postgres")
					for _, action := range []string{"SECURITY DEFINER", "SECURITY INVOKER", "STRICT", "CALLED ON NULL INPUT", "IMMUTABLE", "STABLE", "VOLATILE"} {
						err := enginetest.ExtensionUnitExecute(t, e, admission, enginetest.Statement(t, "ALTER FUNCTION "+wrapper.signature+" "+action))
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "execution attributes are unsupported for native routine providers" || e.CatalogRead().State() != view.State() {
							t.Fatal("SQL wrapper acquired unsupported execution attribute", action, err)
						}
					}
					for _, slot := range []int{0, 1, 2, 3, 4, 5, 8} {
						kernel := view.MustProcedure(header.Members[slot])
						signature := kernel.Name() + "(" + strings.TrimSuffix(strings.Repeat("text,", strings.Count(string(kernel.Signature), ",")+1), ",") + ")"
						for _, action := range []string{"SET application_name = 'ignored'", "RESET application_name", "RESET ALL"} {
							err := enginetest.ExtensionUnitExecute(t, e, admission, enginetest.Statement(t, "ALTER FUNCTION "+signature+" "+action))
							var diagnostic *sqlstate.Error
							if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "execution attributes are unsupported for native routine providers" || e.CatalogRead().State() != view.State() {
								t.Fatal("non-wrapper acquired routine configuration", signature, action, err)
							}
						}
					}
					finish()
				}
				for _, clause := range []string{"SET application_name = 'xml2 configured'", "SET client_min_messages = warning", "SET datestyle = 'SQL, DMY'"} {
					enginetest.Run(t, e, "ALTER FUNCTION "+wrapper.signature+" "+clause)
				}
				if outcome == "partial_entry" {
					enginetest.Run(t, e, "ALTER FUNCTION "+wrapper.signature+" SET transaction_read_only = on")
				}
				current := e.CatalogRead().MustProcedure(original.Address())
				if original.Configuration != nil || current.Configuration == nil || current.Revision() == original.Revision() || current.DeclarationBody != original.DeclarationBody || current.Origin.Installation != original.Origin.Installation || !extension.StockXML2.SupportsRoutineConfiguration(current) {
					t.Fatal("ordinary ALTER lost genuine wrapper/current configuration", original, current)
				}
				enginetest.Run(t, e, "CREATE FUNCTION xml2_save_caller() RETURNS integer LANGUAGE SQL SET application_name = 'xml2 caller' AS 'SELECT 1'")
				sess := enginetest.StartSession(t, e, nil)
				// BEGIN alone leaves the settings nesting level lazy. Demand
				// the genuine SQL owner's SAVE lifecycle before taking the
				// native bridge's exact caller baseline.
				enginetest.SessionSQL(t, sess, "BEGIN")
				enginetest.TransactionRows(t, sess, "SELECT xml2_save_caller()", 1)
				if sess.Settings.NestLevel != 1 {
					t.Fatal("real SQL caller did not establish transaction SAVE base", sess.Settings.NestLevel)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := sess.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(nil)
				if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
					t.Fatal(err)
				}
				if err := core.ScopeSnapshotDataPhase(scope); err != nil {
					t.Fatal(err)
				}
				// SELECT 1 need not demand identity. Capture the genuine
				// caller before comparing it with body-entry recapture.
				e.Mu.Lock()
				caller, err := scope.RoutineAuthorizationLocked()
				if err == nil {
					err = scope.CaptureIdentityLocked(caller)
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				host := core.HostOf(scope)
				beforeRole, beforeActor := op.RoleState(), op.EffectiveUser
				beforeIdentity, beforeInput, beforeOutput := scope.Identity, scope.Input, scope.Output
				beforeValues, beforeActions, beforeNest := maps.Clone(sess.Settings.Values), len(sess.Settings.Actions), sess.Settings.NestLevel
				application, _ := op.Setting("application_name")
				threshold, _ := op.Setting("client_min_messages")
				text, _ := e.CatalogRead().LookupType(sqltype.Text)
				setting := func(name, want string) {
					t.Helper()
					value, err := host.InvokeSetting("current_setting", []sqltype.Value{sqltype.String(text, name)}, text)
					if err != nil {
						t.Fatal(err)
					}
					got, err := value.Text()
					if err != nil || got != want {
						t.Fatal("real invocation setting", name, got, want, err)
					}
				}
				unchanged := func() {
					t.Helper()
					a, _ := op.Setting("application_name")
					level, _ := op.Setting("client_min_messages")
					if op.RoleState() != beforeRole || op.EffectiveUser != beforeActor || scope.Identity.CurrentPrincipal != beforeIdentity.CurrentPrincipal {
						t.Fatal("native SAVE leaked caller authority", op.RoleState(), beforeRole, op.EffectiveUser, beforeActor, scope.Identity.CurrentPrincipal, beforeIdentity.CurrentPrincipal)
					}
					if scope.Input.DateStyle != beforeInput.DateStyle || scope.Output.DateStyle != beforeOutput.DateStyle {
						t.Fatal("native SAVE leaked caller presentation", scope.Input.DateStyle, beforeInput.DateStyle, scope.Output.DateStyle, beforeOutput.DateStyle)
					}
					if a != application || level != threshold || !reflect.DeepEqual(sess.Settings.Values, beforeValues) || len(sess.Settings.Actions) != beforeActions || sess.Settings.NestLevel != beforeNest {
						t.Fatal("native SAVE leaked caller settings", a, application, level, threshold, sess.Settings.Values, beforeValues, len(sess.Settings.Actions), beforeActions, sess.Settings.NestLevel, beforeNest)
					}
				}
				if leave, err := host.EnterNativeRoutineConfiguration(original); err == nil || leave != nil {
					t.Fatal("stale configuration descriptor entered", err)
				}
				unchanged()
				leave, err := host.EnterNativeRoutineConfiguration(current)
				if outcome == "partial_entry" {
					var diagnostic *sqlstate.Error
					if leave != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `parameter "transaction_read_only" cannot be set locally in functions` {
						t.Fatal("wrong genuine partial-entry failure", leave != nil, err)
					}
					unchanged()
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				setting("application_name", "xml2 configured")
				setting("client_min_messages", "warning")
				if scope.Input.DateStyle != (sqltype.DateStyle{Style: sqltype.DateSQL, Order: sqltype.DateDMY}) || scope.Output.DateStyle != scope.Input.DateStyle {
					t.Fatal("native callee presentation stayed stale", scope.Input.DateStyle, scope.Output.DateStyle)
				}
				if op.EffectiveUser != beforeActor || op.RoleState() != beforeRole || scope.Identity.CurrentPrincipal != beforeIdentity.CurrentPrincipal {
					t.Fatal("native callee changed caller authority", op.EffectiveUser, beforeActor, op.RoleState(), beforeRole, scope.Identity.CurrentPrincipal, beforeIdentity.CurrentPrincipal)
				}
				var primary error
				switch outcome {
				case "error":
					primary = errors.New("xml2 body failure")
				case "cancel":
					cancel()
					primary = ctx.Err()
				}
				if err := leave(primary); !errors.Is(err, primary) {
					t.Fatal("native SAVE replaced primary error", err, primary)
				}
				unchanged()
				if outcome != "cancel" {
					setting("application_name", application)
					setting("client_min_messages", threshold)
				}
			})
		}
	}
}

func TestXML2AggregateOwnership(t *testing.T) {
	for _, wrapper := range []struct {
		slot, combine int
		name, signature, inputs, arguments string
	}{
		{7, 7, "xpath_nodeset", "xpath_nodeset(text,text)", "text", "'''<r><a>A</a></r>'''::text"},
		{6, 7, "xpath_nodeset", "xpath_nodeset(text,text,text)", "text,text", "'''<r><a>A</a></r>'''::text,''::text"},
		{9, 9, "xpath_list", "xpath_list(text,text)", "text", "'''<r><a>A</a></r>'''::text"},
	} {
		for _, mode := range []string{"plain", "configured"} {
			t.Run(wrapper.signature+"/"+mode, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE EXTENSION xml2")
				if mode == "configured" {
					enginetest.Run(t, e, "ALTER FUNCTION "+wrapper.signature+" SET application_name = 'xml2 aggregate'")
				}
				enginetest.Run(t, e, "CREATE AGGREGATE xml2_owner_fold("+wrapper.inputs+") (SFUNC="+wrapper.name+",STYPE=text,INITCOND='<r/>',COMBINEFUNC="+wrapper.name+",PARALLEL=SAFE)")
				sql := "SELECT xml2_owner_fold(" + wrapper.arguments + ") AS value FROM t"
				query, source := enginetest.QueryFixture(t, e, sql)
				project := query.Root.(*plan.ProjectNode)
				node := project.Child.(*plan.AggregateNode)
				selected := node.Calls[0].Provider.(*exec.TransitionAggregate)
				view := e.CatalogRead()
				header := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
				call, ok := selected.Call.(plan.InstalledXML2Expr)
				if selected.CombinePlan == nil {
					t.Fatal("real aggregate lacks its declared combine implementation")
				}
				combine, combineOK := selected.CombinePlan.Call.(plan.InstalledXML2Expr)
				if !ok || !combineOK || selected.Support != view.MustProcedure(header.Members[wrapper.slot]) || selected.CombinePlan.Support != view.MustProcedure(header.Members[wrapper.combine]) || call.Procedure != selected.Support || combine.Procedure != selected.CombinePlan.Support || call.Kernel == nil || combine.Kernel == nil || call.Kernel.Origin.Installation != call.Procedure.Origin.Installation || !selected.PartialCapable() {
					t.Fatal("fixture did not bind genuine partial XML2 support/combined frames", selected)
				}
				if _, eligible := exec.ParallelAggregatePlan(query.Root, source); eligible {
					t.Fatal("live XML2 aggregate support entered closed worker fanout")
				}
				if exec.ParallelQueryExpression(call, project.Frame, len(project.Child.Schema())) {
					t.Fatal("XML2 direct evaluator entered the audited worker allowlist")
				}

				_, provider, evaluation, _ := aggregateCombineBindUnit(t, e, sql)
				text, _ := evaluation.Catalog.LookupType(sqltype.Text)
				inputs := []sqltype.Value{sqltype.String(text, "'<r><a>A</a></r>'")}
				if wrapper.slot == 6 {
					inputs = append(inputs, sqltype.String(text, ""))
				}
				state, err := provider.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				if err := provider.Advance(evaluation.Context, state, inputs, evaluation); err != nil {
					t.Fatal("real aggregate continuation/entered body", err)
				}
				originalCall := provider.Call
				originalState := e.CatalogRead().State()
				foreign := engine.New()
				otherName := "xpath_list"
				if wrapper.name == otherName {
					otherName = "xpath_nodeset"
				}
				binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
				other, err := binder.BindFunctionProvider(enginetest.Statement(t, "SELECT "+otherName+"($1::text,$2::text)").(*parser.Select).Targets[0].(parser.FunctionExpr))
				if err != nil {
					t.Fatal(err)
				}
				for _, failure := range []string{"copied_evaluator", "foreign_epoch", "foreign_owner", "foreign_descriptor"} {
					rejected := *provider
					rejected.Call = sepgsqlAggregateCallProbe{Info: expr.InfoOf(originalCall), run: func(c *exec.Context) (sqltype.Value, error) {
						switch failure {
						case "copied_evaluator":
							copied := *c
							return originalCall.Eval(&copied)
						case "foreign_epoch":
							state := catalog.CloneState(c.Catalog.State())
							state.SetEpoch(c.Catalog.Epoch() + 1)
							c.Catalog = c.Catalog.WithState(state)
						case "foreign_owner":
							c.Catalog = foreign.CatalogRead()
						case "foreign_descriptor":
							return other.Eval(c)
						}
						return originalCall.Eval(c)
					}}
					rejectedState, err := rejected.InitWithContext(evaluation.Context, evaluation)
					if err != nil {
						t.Fatal(err)
					}
					before, err := rejected.FinalizeWithContext(evaluation.Context, rejectedState, nil, evaluation)
					if err != nil {
						t.Fatal(err)
					}
					if err := rejected.Advance(evaluation.Context, rejectedState, inputs, evaluation); err == nil {
						t.Fatal("foreign actual aggregate source was accepted", failure)
					}
					after, err := rejected.FinalizeWithContext(evaluation.Context, rejectedState, nil, evaluation)
					if err != nil || !reflect.DeepEqual(before, after) || e.CatalogRead().State() != originalState || !reflect.DeepEqual(provider.Call, originalCall) {
						t.Fatal("rejected source changed accepted state/backing", failure, err)
					}
				}
				if err := provider.Advance(evaluation.Context, state, inputs, evaluation); err != nil {
					t.Fatal("healthy source after failed copies", err)
				}
			})
		}
	}
}

func TestXML2ConfigurationPublication(t *testing.T) {
	t.Run("MixedIndexes", func(t *testing.T) { xml2MixedIndexPublication(t) })
	for _, wrapper := range []struct {
		slot, kernel int
		name, signature, tail string
	}{
		{6, 5, "xpath_nodeset", "xpath_nodeset(text,text,text)", ",'i'"},
		{7, 5, "xpath_nodeset", "xpath_nodeset(text,text)", ""},
		{9, 8, "xpath_list", "xpath_list(text,text)", ""},
	} {
		t.Run(wrapper.signature, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION xml2")
			view := e.CatalogRead()
			header := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
			inner := view.MustProcedure(header.Members[wrapper.kernel])
			innerSignature := inner.Name() + "(" + strings.TrimSuffix(strings.Repeat("text,", strings.Count(string(inner.Signature), ",")+1), ",") + ")"
			// Allocate a genuine noncanonical declaration revision through
			// owner DDL before any consumer captures the inner descriptor.
			enginetest.Run(t, e, "CREATE ROLE xml2_configuration_kernel_owner")
			enginetest.Run(t, e, "ALTER FUNCTION "+innerSignature+" OWNER TO xml2_configuration_kernel_owner")
			enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION "+innerSignature+" FROM PUBLIC")
			enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION "+innerSignature+" TO PUBLIC")
			rowCall := wrapper.name + "(doc,path" + wrapper.tail + ")"
			defaultCall := wrapper.name + "('<r><a>A</a></r>','/r/a'" + wrapper.tail + ")"
			aggregateInputs, aggregateArgs := "text", "'''<r><a>A</a></r>'''::text"
			if wrapper.tail != "" {
				aggregateInputs += ",text"
				aggregateArgs += ",'i'::text"
			}
			for _, q := range []string{
				"CREATE TABLE xml2_config_owned(id integer,doc text,path text,n text DEFAULT "+defaultCall+",CONSTRAINT xml2_config_check CHECK("+rowCall+" <> ''))",
				"INSERT INTO xml2_config_owned(id,doc,path) VALUES(1,'<r><a>A</a></r>','/r/a')",
				"CREATE INDEX xml2_config_index ON xml2_config_owned(("+rowCall+"))",
				"CREATE VIEW xml2_config_view AS SELECT "+rowCall+" AS n FROM xml2_config_owned",
				"CREATE AGGREGATE xml2_config_aggregate("+aggregateInputs+") (SFUNC="+wrapper.name+",STYPE=text,COMBINEFUNC="+wrapper.name+",INITCOND='<r/>')",
				"CREATE VIEW xml2_config_aggregate_view AS SELECT xml2_config_aggregate("+aggregateArgs+") AS n FROM xml2_config_owned",
			} {
				enginetest.Run(t, e, q)
			}
			relation := func(read catalog.Read, name string) *catalog.Relation {
				return read.MustRelation(read.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name}))
			}
			aggregate := func(owner *catalog.Relation) *exec.TransitionAggregate {
				var selected *exec.TransitionAggregate
				plan.VisitCTEQuery(stored.ViewOf(owner).Query().Root, func(node plan.Node) {
					if node, ok := node.(*plan.AggregateNode); ok {
						for _, call := range node.Calls {
							if candidate, ok := call.Provider.(*exec.TransitionAggregate); ok {
								selected = candidate
							}
						}
					}
				}, func(expr.Expr) {}, true)
				if selected == nil {
					t.Fatal("missing actual stored aggregate support")
				}
				return selected
			}
			foreign := engine.New().CatalogRead()
			for _, clause := range []string{"SET client_min_messages = warning", "SET client_min_messages = notice", "RESET ALL"} {
				source := e.CatalogRead()
				before := source.MustProcedure(header.Members[wrapper.slot])
				kernel := source.MustProcedure(header.Members[wrapper.kernel])
				oldTable := relation(source, "xml2_config_owned")
				oldIndex := relation(source, "xml2_config_index")
				oldView := relation(source, "xml2_config_view")
				oldAggregate := relation(source, "xml2_config_aggregate_view")
				defaults := maps.Clone(oldTable.ColumnMeta.Defaults)
				checks := slices.Clone(oldTable.Constraints.Check)
				checkDependencies := slices.Clone(checks[0].Dependencies)
				viewRoot := stored.ViewOf(oldView).Query().Root
				aggregateRoot := stored.ViewOf(oldAggregate).Query().Root
				aggregateDependencies := slices.Clone(stored.ViewOf(oldAggregate).Dependencies())
				definitionCall, ok := oldIndex.SQLIndex.Keys[0].Derived.Definition.(plan.InstalledXML2Expr)
				if !ok || definitionCall.Inline || definitionCall.Procedure != before || definitionCall.Kernel != kernel {
					t.Fatal("source index definition lost strict original authority")
				}
				sourceExecution := oldIndex.SQLIndex.Keys[0].Derived.Execution
				executionCall, ok := sourceExecution.(plan.InstalledXML2Expr)
				if !ok || executionCall.Inline != (before.Configuration == nil) {
					t.Fatal("missing genuine cooked source index execution", executionCall)
				}
				sourceCombine := aggregate(oldAggregate).CombinePlan
				if sourceCombine == nil {
					t.Fatal("missing actual selected combine frame")
				}
				sourceCombineCall, ok := sourceCombine.Call.(plan.InstalledXML2Expr)
				if !ok || sourceCombineCall.Procedure != sourceCombine.Support || sourceCombineCall.Kernel != kernel {
					t.Fatal("missing actual original combine body/kernel")
				}
				if wrapper.slot == 6 && sourceCombine.Support.Address() == before.Address() {
					t.Fatal("three-argument SFUNC did not select distinct two-argument COMBINE")
				}
				physical, rows := e.Storage.IndexRoots[oldIndex.Address()], e.Storage.Relations[oldTable.Address()]
				if physical == nil {
					t.Fatal("missing genuine source index root", oldIndex.Address())
				}
				if len(physical.Entries) == 0 {
					t.Fatal("source index is not genuinely populated", oldIndex.Address(), physical, rows)
				}
				canonical := header.Installation.Declarations[wrapper.kernel]
				if kernel.Revision() == canonical.Revision() {
					t.Fatal("source kernel retained canonical revision after real owner transition", kernel.Address(), kernel.Revision(), canonical.Revision(), kernel.Owner, canonical.(*catalog.Procedure).Owner)
				}
				enginetest.Run(t, e, "ALTER FUNCTION "+wrapper.signature+" "+clause)
				target := e.CatalogRead()
				after := target.MustProcedure(before.Address())
				if exec.SameRoutineDeclaration(before, after) {
					t.Fatal("configuration publication weakened declaration equality")
				}
				if _, err := exec.ValidateStoredRoutineDescriptor(target, before); err == nil {
					t.Fatal("historical carrier acquired current configuration authority")
				}
				if e.Storage.IndexRoots[oldIndex.Address()] != physical || e.Storage.Relations[oldTable.Address()] != rows {
					t.Fatal("configuration publication rebuilt physical data")
				}
				for _, old := range []*catalog.Relation{oldTable, oldIndex, oldView, oldAggregate} {
					current := target.MustRelation(old.Address())
					if current == old || current.Revision() <= old.Revision() || !stored.ScalarRelationSuccessor(target, old, current) {
						t.Fatal("configuration consumer lost checked successor chain", old.Name())
					}
					if err := target.Owner().Provider(ids.RelationClassID).Validate(target, current); err != nil {
						t.Fatal("published consumer invalid", old.Name(), err)
					}
					found := false
					if err := stored.VisitDefinitionExpressions(current, func(node expr.Expr) {
						plan.WalkExpression(node, func(node expr.Expr) {
							call, ok := node.(plan.InstalledXML2Expr)
							if !ok || call.Ref.Address != before.Address() {
								return
							}
							found = true
							properties := expr.Properties{Volatility: expr.Immutable}
							for _, arg := range call.Args {
								properties = expr.MergeProperties(properties, arg.Properties())
							}
							if call.Inline {
								t.Fatal("publication installed an inline definition", old.Name())
							}
							if call.Properties() != properties {
								t.Fatal("publication lost exact mapped child demand", old.Name(), call.Properties(), properties)
							}
							if call.Procedure != after || call.RoutineReference != after || call.Kernel != kernel || call.Ref.Domain() != target.Domain() || call.Ref.Epoch != target.Epoch() {
								t.Fatal("publication lost selected outer/original kernel", old.Name())
							}
						})
					}); err != nil || !found && old != oldAggregate {
						t.Fatal("missing published wrapper occurrence", old.Name(), err)
					}
				}
				currentIndex := target.MustRelation(oldIndex.Address())
				currentExecution, ok := currentIndex.SQLIndex.Keys[0].Derived.Execution.(plan.InstalledXML2Expr)
				if !ok || currentExecution.Inline || currentExecution.Procedure != after || currentExecution.Kernel != kernel ||
					!reflect.DeepEqual(oldIndex.SQLIndex.Keys[0].Derived.Execution, sourceExecution) {
					t.Fatal("index publication lost conservative execution/original source pair")
				}
				currentTable := target.MustRelation(oldTable.Address())
				for _, dependency := range currentTable.Constraints.Check[0].Dependencies {
					if dependency.Ref.Address == before.Address() && (dependency.Revision != after.Revision() || dependency.Purpose != catalog.DefinitionDependency) ||
						dependency.Ref.Address == kernel.Address() && (dependency.Revision != kernel.Revision() || dependency.Purpose != catalog.DefinitionDependency) {
						t.Fatal("publication changed CHECK capture lane", dependency)
					}
				}
				support := aggregate(target.MustRelation(oldAggregate.Address()))
				call, ok := support.Call.(plan.InstalledXML2Expr)
				if !ok || support.Support != after || call.Procedure != after || call.RoutineReference != after || call.Kernel != kernel {
					t.Fatal("stored aggregate lost actual selected body/current configuration")
				}
				combine := support.CombinePlan
				if combine == nil {
					t.Fatal("publication lost actual selected combine frame")
				}
				combineCall, ok := combine.Call.(plan.InstalledXML2Expr)
				expectedCombine := sourceCombine.Support
				if expectedCombine.Address() == before.Address() {
					expectedCombine = after
				}
				if !ok || combine.Support != expectedCombine || combineCall.Procedure != expectedCombine ||
					combineCall.Kernel != sourceCombineCall.Kernel || !reflect.DeepEqual(sourceCombine.Call, sourceCombineCall) {
					t.Fatal("publication lost exact distinct combine support/original body capture")
				}
				for _, dependency := range stored.ViewOf(target.MustRelation(oldAggregate.Address())).Dependencies() {
					if dependency.Ref.Address == before.Address() && (dependency.Revision != after.Revision() || dependency.Purpose != catalog.AggregateSupportDependency) ||
						dependency.Ref.Address == kernel.Address() && (dependency.Revision != kernel.Revision() || dependency.Purpose != catalog.AggregateSupportDependency) {
						t.Fatal("publication changed aggregate support capture lane", dependency)
					}
				}
				if !reflect.DeepEqual(defaults, oldTable.ColumnMeta.Defaults) || !reflect.DeepEqual(checks, oldTable.Constraints.Check) ||
					!slices.Equal(checkDependencies, oldTable.Constraints.Check[0].Dependencies) || stored.ViewOf(oldView).Query().Root != viewRoot ||
					stored.ViewOf(oldAggregate).Query().Root != aggregateRoot || source.Get(before.Address()) != before || source.Get(kernel.Address()) != kernel {
					t.Fatal("configuration publication mutated original source/backing")
				}
				// Replay the real checked predecessor/successor boundary with
				// corrupted private source manifests. No failure may publish.
				inlineExpression := func(node expr.Expr) expr.Expr {
					next, changed, err := stored.MapExpression(source, node, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil,
						func(node expr.Expr) (expr.Expr, bool, error) {
							call, ok := node.(plan.InstalledXML2Expr)
							if !ok || call.Ref.Address != before.Address() {
								return node, false, nil
							}
							call.Inline = true
							return call, true, nil
						})
					if err != nil || !changed {
						t.Fatal("construct actual injected inline occurrence", err)
					}
					return next
				}
				for _, kind := range []string{"missing-check-inner", "stale-check-inner", "wrong-check-purpose", "foreign-check-inner", "inline-default", "inline-check", "inline-index-definition", "configured-inline-index", "missing-view-inner", "missing-view-outer", "wrong-aggregate-purpose", "missing-combine-support", "wrong-combine-purpose"} {
					if kind == "configured-inline-index" && before.Configuration == nil {
						continue
					}
					state := catalog.CloneState(source.State())
					if kind == "inline-default" {
						bad := *oldTable
						bad.ColumnMeta.Defaults = maps.Clone(oldTable.ColumnMeta.Defaults)
						for attribute, definition := range bad.ColumnMeta.Defaults {
							definition.Expression = inlineExpression(definition.Expression)
							bad.ColumnMeta.Defaults[attribute] = definition
						}
						state.SetDescriptor(bad.Address(), &bad)
					} else if kind == "configured-inline-index" || kind == "inline-index-definition" {
						bad := *oldIndex
						index := *oldIndex.SQLIndex
						index.Keys = slices.Clone(index.Keys)
						derived := *index.Keys[0].Derived
						if kind == "inline-index-definition" {
							derived.Definition = inlineExpression(derived.Definition)
						} else {
							call := executionCall
							call.Inline = true
							derived.Execution = call
						}
						index.Keys[0].Derived = &derived
						bad.SQLIndex = &index
						state.SetDescriptor(bad.Address(), &bad)
					} else if strings.Contains(kind, "check") {
						bad := *oldTable
						bad.Constraints.Check = slices.Clone(oldTable.Constraints.Check)
						bad.Constraints.Check[0].Dependencies = slices.Clone(checkDependencies)
						if kind == "inline-check" {
							bad.Constraints.Check[0].Expression = inlineExpression(bad.Constraints.Check[0].Expression)
						}
						deps := bad.Constraints.Check[0].Dependencies
						for i := range deps {
							if deps[i].Ref.Address != kernel.Address() {
								continue
							}
							switch kind {
							case "missing-check-inner":
								deps = append(deps[:i], deps[i+1:]...)
							case "stale-check-inner":
								deps[i].Revision++
							case "wrong-check-purpose":
								deps[i].Purpose = catalog.OperandDependency
							case "foreign-check-inner":
								deps[i].Ref = foreign.Domain().Ref(deps[i].Ref.Epoch, deps[i].Ref.Address)
							}
							break
						}
						bad.Constraints.Check[0].Dependencies = deps
						state.SetDescriptor(bad.Address(), &bad)
					} else {
						original := oldView
						if kind == "wrong-aggregate-purpose" || strings.Contains(kind, "combine") {
							original = oldAggregate
						}
						bad := *original
						bad.View = stored.ViewPayload(stored.ViewOf(original).With(func(parts *stored.ViewParts) {
							parts.Dependencies = slices.Clone(parts.Dependencies)
							if kind == "missing-view-inner" || kind == "missing-view-outer" {
								address := kernel.Address()
								if kind == "missing-view-outer" {
									address = before.Address()
								}
								parts.Dependencies = slices.DeleteFunc(parts.Dependencies, func(d catalog.ExpressionDependency) bool { return d.Ref.Address == address })
							} else if kind == "missing-combine-support" {
								count := len(parts.Dependencies)
								parts.Dependencies = slices.DeleteFunc(parts.Dependencies, func(d catalog.ExpressionDependency) bool {
									return d.Ref.Address == sourceCombine.Support.Address() && d.Purpose == catalog.AggregateSupportDependency
								})
								if count-len(parts.Dependencies) != 1 {
									t.Fatal("combine fixture did not remove its single original support capture")
								}
								if sourceCombine.Support.Address() == before.Address() {
									for _, dependency := range parts.Dependencies {
										if dependency.Ref.Address == before.Address() {
											t.Fatal("shared SFUNC/COMBINE fixture retained a selected-wrapper capture")
										}
									}
								}
							} else {
								address := before.Address()
								if kind == "wrong-combine-purpose" {
									address = sourceCombine.Support.Address()
								}
								for i := range parts.Dependencies {
									if parts.Dependencies[i].Ref.Address == address {
										parts.Dependencies[i].Purpose = catalog.DefinitionDependency
									}
								}
							}
						}))
						state.SetDescriptor(bad.Address(), &bad)
					}
					badSource := source.WithState(state)
					destination := catalog.CloneState(state)
					destination.SetDescriptor(after.Address(), after)
					e.Mu.Lock()
					publication, err := stored.RefreshStoredScalarRoutine(badSource, source.WithState(destination), before, after, e.Catalog.RevisionLocked)
					e.Mu.Unlock()
					if err == nil || publication != nil || e.CatalogRead().State() != target.State() || e.Storage.IndexRoots[oldIndex.Address()] != physical ||
						!slices.Equal(stored.ViewOf(oldAggregate).Dependencies(), aggregateDependencies) || stored.ViewOf(oldAggregate).Query().Root != aggregateRoot ||
						stored.ViewOf(oldView).Query().Root != viewRoot {
						t.Fatal("invalid configuration publication escaped", kind, err)
					}
				}
				boundary := catalog.CloneState(source.State())
				boundary.SetDescriptor(after.Address(), after)
				destination := source.WithState(boundary)
				for _, kind := range []string{"foreign-source", "foreign-target", "source-epoch", "target-epoch", "forged-before", "forged-after", "body", "cohort", "security", "strict", "invalid-configuration"} {
					origin, successor, predecessor, candidate := source, destination, before, after
					switch kind {
					case "foreign-source":
						origin = foreign
					case "foreign-target":
						successor = foreign
					case "source-epoch":
						origin = source.WithState(catalog.NewState(source.Epoch()+1))
					case "target-epoch":
						successor = destination.WithState(catalog.NewState(destination.Epoch()+1))
					case "forged-before":
						copy := *before
						predecessor = &copy
					case "forged-after":
						copy := *after
						candidate = &copy
					default:
						copy := *after
						switch kind {
						case "body":
							slot := 6
							if slot == wrapper.slot {
								slot = 7
							}
							copy.DeclarationBody = source.MustProcedure(header.Members[slot]).DeclarationBody
						case "cohort":
							origin := *copy.Origin
							installation := *origin.Installation
							origin.Installation = &installation
							copy.Origin = &origin
						case "security":
							copy.SecurityDefiner = true
						case "strict":
							copy.Strict = false
						case "invalid-configuration":
							copy.Configuration = catalog.NewRoutineConfiguration([]string{"broken"})
						}
						state := catalog.CloneState(boundary)
						state.SetDescriptor(copy.Address(), &copy)
						successor, candidate = destination.WithState(state), &copy
					}
					e.Mu.Lock()
					publication, err := stored.RefreshStoredScalarRoutine(origin, successor, predecessor, candidate, e.Catalog.RevisionLocked)
					e.Mu.Unlock()
					if err == nil || publication != nil || e.CatalogRead().State() != target.State() || e.Storage.IndexRoots[oldIndex.Address()] != physical ||
						!reflect.DeepEqual(defaults, oldTable.ColumnMeta.Defaults) || !slices.Equal(checkDependencies, oldTable.Constraints.Check[0].Dependencies) {
						t.Fatal("invalid configuration boundary escaped", kind, err)
					}
				}
				for _, q := range []string{
					"INSERT INTO xml2_config_owned(id,doc,path) VALUES(2,'<r><a>B</a></r>','/r/a')",
					"SELECT * FROM xml2_config_view",
					"SELECT * FROM xml2_config_aggregate_view",
					"DELETE FROM xml2_config_owned WHERE id=2",
				} {
					enginetest.Run(t, e, q)
				}
			}
		})
	}
}
