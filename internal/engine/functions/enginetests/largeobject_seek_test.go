package enginetests_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestLargeObjectSeekCall(t *testing.T) {
	t.Run("typed-recipe", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		replacement := make(map[sqltype.ID]*sqltype.Descriptor)
		for _, d := range sqltype.Builtins() { replacement[d.ID()] = d }
		for _, tc := range []struct {
			text string
			oid ids.OID
			result sqltype.ID
			formals []sqltype.ID
		}{
			{"lo_lseek(NULL,-1,0)", 956, sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int4}},
			{"lo_lseek64(NULL,-1,0)", 3170, sqltype.Int8, []sqltype.ID{sqltype.Int4, sqltype.Int8, sqltype.Int4}},
			{"lo_tell(NULL)", 958, sqltype.Int4, []sqltype.ID{sqltype.Int4}},
			{"lo_tell64(NULL)", 3171, sqltype.Int8, []sqltype.ID{sqltype.Int4}},
		} {
			node, _, ctx := strictNullUnitBind(t, e, op, scope, tc.text)
			n := node.(functions.LargeObjectCall)
			d, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || d == nil || d.Address().ObjectID != tc.oid || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 'u' { t.Fatal("seek provider identity", d, err) }
			if n.ResultUsage().Type.ID() != tc.result || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren { t.Fatal("typed nonfolding recipe", n) }
			for i, child := range n.Args { if child.ResultUsage().Type.ID() != tc.formals[i] { t.Fatal("signed formal", i, child.ResultUsage()) } }
			value, err := n.Eval(&ctx)
			if err != nil || !value.IsNull() || value.Type() != n.ResultUsage().Type { t.Fatal("strict typed NULL", value, err) }
			shape, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || shape.Count != len(tc.formals) || shape.Check(n, len(n.Args)) != nil { t.Fatal("capture", shape, err) }
			for i, want := range n.Args {
				got, err := n.CaptureOperand(i)
				if err != nil || !reflect.DeepEqual(got, want) { t.Fatal("capture operand", i, err) }
			}
			for _, i := range []int{-1, len(n.Args)} { if _, err := n.CaptureOperand(i); err == nil { t.Fatal("accepted nonexistent operand", i) } }
			rendered, err := n.Render()
			if err != nil || rendered.Name != n.Name || rendered.Form != expr.FormCall || len(rendered.Inputs) != len(n.Args) || !reflect.DeepEqual(rendered.Inputs, n.Args) { t.Fatal("render", rendered, err) }
			children := append([]expr.Expr(nil), n.Args...)
			rebuilt, err := n.Rebuild(n.Info, children)
			if err != nil { t.Fatal(err) }
			copy := rebuilt.(functions.LargeObjectCall)
			children[0] = nil
			if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.RoutineReference != n.RoutineReference { t.Fatal("rebuild borrowed operands or lost provider") }
			if _, err := n.Rebuild(n.Info, nil); err == nil { t.Fatal("accepted truncated operands") }
			// Composite rebuilds validate arity; the capture boundary rejects
			// missing written operands before the engine can publish them.
			for i := range n.Args {
				invalid := n
				invalid.Args = append([]expr.Expr(nil), n.Args...)
				invalid.Args[i] = nil
				operands, ok, err := invalid.Capture(expr.CaptureFunction)
				if err != nil || !ok { t.Fatal("capture admission", i, err) }
				if err := operands.Check(invalid, len(invalid.Args)); err == nil { t.Fatal("capture accepted nil operand", i) }
				if _, err := operands.At(invalid, i); err == nil { t.Fatal("capture accessor accepted nil operand", i) }
				if _, err := invalid.CaptureOperand(i); err == nil { t.Fatal("indexed capture accepted nil operand", i) }
			}
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
			if err != nil || !changed { t.Fatal("retag", changed, err) }
			next := retagged.(functions.LargeObjectCall)
			if next.Usage.Type != replacement[tc.result] || next.RoutineReference != n.RoutineReference || &next.Args[0] == &n.Args[0] { t.Fatal("retag result/provider/ownership") }
			for i, child := range next.Args { if child.ResultUsage().Type != replacement[tc.formals[i]] { t.Fatal("retag formal", i) } }
			value, err = next.Eval(&ctx)
			if err != nil || !value.IsNull() || value.Type() != replacement[tc.result] || n.Usage.Type == replacement[tc.result] { t.Fatal("retag changed original", value, err) }
		}
		// Real statement authority and descriptor state supply the result; binding
		// and typed NULL evaluation above have allocated no descriptor slots.
		if _, err := core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "lo_from_bytea", OID: 910293, Data: "abcdef"}); err != nil { t.Fatal(err) }
		open, _, ctx := strictNullUnitBind(t, e, op, scope, "lo_open(910293::oid,131072)")
		value, err := open.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		if fd, err := value.Integer(); err != nil || fd != 0 { t.Fatal("binding allocated descriptor", value, err) }
		for _, tc := range []struct { text string; want int64; typ sqltype.ID }{
			{"lo_tell(0)", 0, sqltype.Int4},
			{"lo_lseek(0,2,0)", 2, sqltype.Int4},
			{"lo_lseek64(0,1,1)", 3, sqltype.Int8},
			{"lo_lseek64(0,4294967296::bigint,0)", 4294967296, sqltype.Int8},
			{"lo_tell64(0)", 4294967296, sqltype.Int8},
		} {
			node, _, ctx := strictNullUnitBind(t, e, op, scope, tc.text)
			before := node.(functions.LargeObjectCall)
			value, err := node.Eval(&ctx)
			if err != nil { t.Fatal(err) }
			if got, err := value.Integer(); err != nil || got != tc.want || value.Type().ID() != tc.typ { t.Fatal("position result narrowed", value, err) }
			if !reflect.DeepEqual(before, node) { t.Fatal("recipe acquired cursor or transaction authority") }
		}
		invalid, _, ctx := strictNullUnitBind(t, e, op, scope, "lo_lseek64(-1,-1,3)")
		_, err = invalid.Eval(&ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != "invalid large-object descriptor: -1" { t.Fatal("FD converted through OID", err) }
		original := scope.TxRef
		scope.TxRef = original.Domain().Tx(original.Session(), original.ID(), original.Epoch()+1, original.Attachment(), original.Diagnostic())
		_, err = core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "lo_tell64", Descriptor: 0})
		scope.TxRef = original
		if !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("runtime crossed epoch", err) }
	})
	t.Run("runtime-demand", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		iteration := int64(0)
		for _, text := range []string{
			"lo_lseek(NULL,nextval('strict_null_steps'::regclass)::integer,0)",
			"lo_lseek(nextval('strict_null_steps'::regclass)::integer,NULL,0)",
			"lo_lseek(0,nextval('strict_null_steps'::regclass)::integer,NULL)",
			"lo_lseek64(NULL,nextval('strict_null_steps'::regclass),0)",
			"lo_lseek64(nextval('strict_null_steps'::regclass)::integer,NULL,0)",
			"lo_lseek64(0,nextval('strict_null_steps'::regclass),NULL)",
		} {
			node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
			for range 2 {
				iteration++
				value, err := node.Eval(&ctx)
				if err != nil || !value.IsNull() { t.Fatal("runtime NULL", value, err) }
				current, _, currentCtx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
				value, err = current.Eval(&currentCtx)
				if err != nil { t.Fatal(err) }
				if n, err := value.Integer(); err != nil || n != iteration { t.Fatal("volatile child repeated or suppressed", value, err) }
			}
		}
		for _, text := range []string{"lo_lseek(NULL,1/0,0)", "lo_lseek64(0,NULL,1/0)"} {
			node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
			_, err := node.Eval(&ctx)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22012" || d.Message != "division by zero" { t.Fatal("runtime NULL skipped demanded child", err) }
		}
	})
	for _, planner := range []struct {
		name string
		run func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			for _, text := range []string{
				"lo_lseek(NULL,nextval('strict_null_steps'::regclass)::integer,0)",
				"lo_lseek64(0,nextval('strict_null_steps'::regclass),NULL)",
				"lo_tell(NULL)", "lo_tell64(NULL)",
			} {
				e, op, scope := strictNullUnitFixture(t)
				node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
				planned, _, err := planner.run(&ctx, node, true)
				if err != nil { t.Fatal(err) }
				for range 2 {
					value, err := planned.Eval(&ctx)
					if err != nil || !value.IsNull() || value.Type().ID() != node.ResultUsage().Type.ID() { t.Fatal("planning typed NULL", value, err) }
				}
				e.Mu.Lock()
				called := e.Storage.Sequences[sequence.Address].Called
				e.Mu.Unlock()
				if called { t.Fatal("known NULL executed volatile sibling") }
			}
			for _, text := range []string{"lo_lseek($1::integer,1/$2::integer,0)", "lo_lseek64(0,$1::bigint,1/$2::integer)"} {
				e, op, scope := strictNullUnitFixture(t)
				node, binder, ctx := strictNullUnitBind(t, e, op, scope, text)
				zero, err := sqltype.Integer(binder.Params.Parameters[2].Type, 0)
				if err != nil { t.Fatal(err) }
				ctx.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type), zero}
				planned, _, err := planner.run(&ctx, node, true)
				if err == nil { _, err = planned.Eval(&ctx) }
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22012" || d.Message != "division by zero" { t.Fatal("NULL suppressed child planning error", err) }
			}
		})
	}
	t.Run("distinct-expansions", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		first, binder, _ := strictNullUnitBind(t, e, op, scope, "lo_tell(NULL)")
		raw := enginetest.Statement(t, "SELECT lo_tell(NULL)").(*parser.Select).Targets[0]
		e.Mu.Lock()
		second, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		e.Mu.Unlock()
		if err != nil { t.Fatal(err) }
		left, right := first.(functions.LargeObjectCall), second.(functions.LargeObjectCall)
		if left.Source != right.Source || left.Occurrence == 0 || right.Occurrence == 0 || left.Occurrence == right.Occurrence { t.Fatal("distinct call expansion provenance", left.Info, right.Info) }
		a, aOK := left.Args[0].(plan.LiteralExpr)
		b, bOK := right.Args[0].(plan.LiteralExpr)
		if !aOK || !bOK || a.Source != b.Source || a.Occurrence == 0 || b.Occurrence == 0 || a.Occurrence == b.Occurrence { t.Fatal("distinct operand expansion provenance", left.Args[0], right.Args[0]) }
		rebuilt, err := right.Rebuild(right.Info, right.Args)
		if err != nil || !reflect.DeepEqual(rebuilt, right) { t.Fatal("rebuild changed exact expansion provenance", err) }
		b.Occurrence = a.Occurrence
		if !reflect.DeepEqual(a, b) { t.Fatal("expansion changed immutable operand", a, b) }
		right.Occurrence = left.Occurrence
		right.Args = []expr.Expr{b}
		if !reflect.DeepEqual(left, right) { t.Fatal("expansion changed provider/logical contract", left, right) }
	})
	t.Run("definition-ownership", seekRecipeOwnership)
}

func seekRecipeOwnership(t *testing.T) {
	for _, tc := range []struct { name, call string; oid ids.OID; operand int }{
		{"lo_lseek", "lo_lseek(0,COALESCE(-(7::integer),0),1)", 956, 1},
		{"lo_lseek64", "lo_lseek64(0,COALESCE(-(7::integer),0),1)", 3170, 1},
		{"lo_tell", "lo_tell(COALESCE(-(7::integer),0))", 958, 0},
		{"lo_tell64", "lo_tell64(COALESCE(-(7::integer),0))", 3171, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "BEGIN")
			// Publish incidental bytes through an actual executed statement.
			// Parse/Bind protocol scopes do not complete runtime catalog effects.
			enginetest.SessionSQL(t, s, "SELECT lo_from_bytea(910294::oid,'abcdef'::bytea)").Rows.Close()
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				host := core.HostOf(scope)
				open, err := host.LargeObject(functions.LargeObjectRequest{Name: "lo_open", OID: 910294, Mode: 131072})
				if err != nil { return err }
				if open.Integer != 0 { t.Fatal("unexpected initial descriptor", open) }
				_, err = host.LargeObject(functions.LargeObjectRequest{Name: "lo_lseek64", Descriptor: 0, Offset: 3})
				return err
			})
			assertQuiet := func() {
				command(func(scope *core.StatementScope) error {
					host := core.HostOf(scope)
					out, err := host.LargeObject(functions.LargeObjectRequest{Name: "lo_tell64", Descriptor: 0})
					if err != nil { return err }
					if out.Position != 3 { t.Fatal("planning changed actual cursor", out) }
					open, err := host.LargeObject(functions.LargeObjectRequest{Name: "lo_open", OID: 910294, Mode: 131072})
					if err != nil { return err }
					if open.Integer != 1 { t.Fatal("planning allocated a descriptor", open) }
					_, err = host.LargeObject(functions.LargeObjectRequest{Name: "lo_close", Descriptor: open.Integer})
					return err
				})
			}
			parse := func() {
				command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("seek_recipe", "SELECT "+tc.call, nil); return err })
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error { _, err := scope.BindProtocolPortal(name, "seek_recipe", &enginetest.ProtocolReader{Data: []byte{0,0,0,0,0,0}}); return err })
			}
			parse()
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("seek_recipe")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil { t.Fatal("missing genuine definition owners") }
			analysis, owner := definition.Description.Analysis, definition.Description.Folds
			parsedAnalysis := *analysis
			parsedAnalysis.Dependencies = append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
			parsedAnalysis.FoldTypes = maps.Clone(analysis.FoldTypes)
			parsedAnalysis.FoldCallables = maps.Clone(analysis.FoldCallables)
			parsedAnalysis.Targets = maps.Clone(analysis.Targets)
			parsedAnalysis.Sources = maps.Clone(analysis.Sources)
			parsedAnalysis.Types = maps.Clone(analysis.Types)
			parsedAnalysis.FoldRelations = maps.Clone(analysis.FoldRelations)
			parsedAnalysis.Casts = maps.Clone(analysis.Casts)
			if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || len(owner.Current().Dependencies) != 0 { t.Fatal("Parse evaluated operands or lost fold owner") }
			assertQuiet()
			command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolStatement("seek_recipe"); return err })
			if registry.Lookup("seek_recipe") != definition || definition.Description.Folds != owner || !owner.Live() { t.Fatal("Describe replaced definition fold owner") }
			if current := owner.Current(); current.Generation != 0 || len(current.Entries) != 0 || len(current.Dependencies) != 0 { t.Fatal("Describe published operand folds", current) }
			if !reflect.DeepEqual(*analysis, parsedAnalysis) { t.Fatal("Describe mutated Parse analysis") }
			// Describe refreshes immutable analysis, not the definition fold owner.
			// Retain the actual post-Describe analysis that Bind consumes.
			analysis = definition.Description.Analysis
			view := e.CatalogRead()
			if analysis == nil || definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.PlanOwner != s.SessionToken || analysis.Domain != e.Catalog.Domain || analysis.Epoch != view.Epoch() || analysis.Session != s.SessionToken { t.Fatal("Describe lost actual analysis authority") }
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid}
			routine := analysis.FoldCallables[address]
			if routine == nil || routine.Address() != address { t.Fatal("Describe lost selected callable") }
			foundDependency := false
			for _, dependency := range analysis.Dependencies {
				if dependency.Ref == e.Catalog.Domain.Ref(view.Epoch(), address) && dependency.Revision == routine.Revision() { foundDependency = true }
			}
			if !foundDependency { t.Fatal("Describe lost selected provider dependency") }
			assertQuiet()
			bind("seek_first")
			assertQuiet()
			first, accepted := registry.Binding("seek_first"), owner.Current()
			if first == nil || first.Resource == nil || first.Resource.Root == nil || first.Folds == nil || len(accepted.Entries) == 0 || len(first.Folds.Entries) != len(accepted.Entries) || first.Entry.Description.Analysis != analysis || first.Entry.Description.Folds != owner || owner.Analysis != analysis || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("Bind lost owner/generation/backing") }
			root := first.Resource.Root
			if len(root.Expressions) != 1 { t.Fatal("missing scalar recipe") }
			node, ok := root.Expressions[0].(functions.LargeObjectCall)
			if !ok || node.Name != tc.name || node.PlanningFold() != expr.FoldNever { t.Fatal("volatile call folded") }
			declaration, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || declaration != routine { t.Fatal("Bind changed described provider identity", declaration, err) }
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), declaration); err != nil { t.Fatal(err) }
			if len(accepted.Dependencies) == 0 { t.Fatal("missing accepted dependencies") }
			if len(first.Folds.Dependencies) != len(accepted.Dependencies) || &first.Folds.Dependencies[0] != &accepted.Dependencies[0] { t.Fatal("Bind lost accepted dependency backing") }
			for _, dependency := range accepted.Dependencies { if dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != view.Epoch() { t.Fatal("dependency crossed incarnation") } }
			for _, entry := range accepted.Entries { if entry.Callable != nil && ids.OID(entry.Callable.RoutineOID()) == tc.oid { t.Fatal("volatile result retained") } }
			child := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "-7")
			if child.Source == (expr.FoldSourceOccurrence{}) || child.Frame != child.Source.Frame || len(child.Inputs) == 0 { t.Fatal("child provenance/inputs absent", child) }
			provider, ok := child.Callable.(*catalog.Procedure)
			if !ok || provider.Volatility != expr.Immutable { t.Fatal("child provider", child.Callable) }
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), provider); err != nil { t.Fatal(err) }
			found := false
			for _, input := range child.Inputs { if input.Value.Type() != nil { if n, err := input.Value.Integer(); err == nil && n == 7 { found = true } } }
			if !found { t.Fatal("child lost original input") }
			index := -1
			for i, entry := range accepted.Entries { if enginetest.SamePublishedFold(entry, child) { index = i; break } }
			if index < 0 { t.Fatal("child absent from accepted generation") }
			bind("seek_second")
			assertQuiet()
			second := registry.Binding("seek_second")
			if second == nil || second.Resource == nil || second.Resource.Root == nil || second.Folds == nil || len(second.Resource.Root.Expressions) != 1 || len(second.Folds.Entries) != len(accepted.Entries) || len(second.Folds.Dependencies) != len(accepted.Dependencies) { t.Fatal("missing second accepted graph/folds") }
			if second == first || second.Resource == first.Resource || second.Resource.Root == root || second.Entry.Description.Analysis != analysis || second.Entry.Description.Folds != owner || second.Folds.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &second.Folds.Dependencies[0] != &accepted.Dependencies[0] || !enginetest.SamePublishedFold(second.Folds.Entries[index], child) || len(second.Folds.Entries[index].Inputs) != len(child.Inputs) || &second.Folds.Entries[index].Inputs[0] != &accepted.Entries[index].Inputs[0] { t.Fatal("independent graph lost accepted immutable backing") }
			// Accepted folds keep definition provenance across distinct graphs.
			// Separately allocated expansions are checked above; compare this
			// operand's complete immutable contract without conflating ordinals.
			next, ok := second.Resource.Root.Expressions[0].(functions.LargeObjectCall)
			if !ok { t.Fatal("second binding lost volatile recipe", second.Resource.Root.Expressions[0]) }
			left, leftOK := node.Args[tc.operand].(plan.LiteralExpr)
			right, rightOK := next.Args[tc.operand].(plan.LiteralExpr)
			if !leftOK || !rightOK || left.Source != right.Source || left.Occurrence == 0 || right.Occurrence == 0 { t.Fatal("bound operand provenance", node.Args[tc.operand], next.Args[tc.operand]) }
			right.Occurrence = left.Occurrence
			if !reflect.DeepEqual(left, right) { t.Fatal("independent expansion changed immutable operand contract", left, right) }
			secondRoot := second.Resource.Root
			assertRetained := func(generation uint64) {
				t.Helper()
				if definition.Description.Analysis != analysis || definition.Description.Folds != owner || owner.Analysis != analysis { t.Fatal("retained definition changed analysis/fold owner") }
				if first.Resource.Root != root || second.Resource.Root != secondRoot || !reflect.DeepEqual(root.Expressions[0], node) || !reflect.DeepEqual(secondRoot.Expressions[0], next) { t.Fatal("retained binding graph or exact provenance changed") }
				for _, binding := range []*core.PreparedBinding{first, second} {
					if binding.Entry.Description.Analysis != analysis || binding.Entry.Description.Folds != owner || binding.Folds.Generation != accepted.Generation || len(binding.Folds.Entries) != len(accepted.Entries) || len(binding.Folds.Dependencies) != len(accepted.Dependencies) || &binding.Folds.Entries[0] != &accepted.Entries[0] || &binding.Folds.Dependencies[0] != &accepted.Dependencies[0] || !enginetest.SamePublishedFold(binding.Folds.Entries[index], child) || &binding.Folds.Entries[index].Inputs[0] != &child.Inputs[0] { t.Fatal("retained binding lost accepted owner/generation/entries/inputs/dependencies") }
				}
				current := owner.Current()
				if current.Generation != generation || len(current.Entries) != len(accepted.Entries) || len(current.Dependencies) != len(accepted.Dependencies) || &current.Entries[0] != &accepted.Entries[0] || &current.Dependencies[0] != &accepted.Dependencies[0] { t.Fatal("definition lost accepted generation/immutable backing") }
			}
			assertRetained(accepted.Generation)
			// Publication uses current admitted authorization under Engine.Mu,
			// including this transaction's private catalog, like real planning.
			publication := func() *core.FoldPublication {
				op, err := s.Acquire(context.Background())
				if err != nil { t.Fatal(err) }
				defer op.Done()
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil { t.Fatal(err) }
				environment, err := e.PreparedEnvironmentLocked(auth, true)
				if err != nil { t.Fatal(err) }
				p, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
				if err != nil || p == nil { t.Fatal("missing actual publication", err) }
				return p
			}()
			if owner.Current().Generation != accepted.Generation { t.Fatal("publication setup changed accepted generation") }
			assertRetained(accepted.Generation)
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("seek_recipe") })
			retired := owner.Current()
			if owner.Live() || registry.Lookup("seek_recipe") != nil || retired.Generation != accepted.Generation+1 { t.Fatal("definition retirement lost actual owner generation") }
			assertRetained(retired.Generation)
			func() {
				op, err := s.Acquire(context.Background())
				if err != nil { t.Fatal(err) }
				defer op.Done()
				e.Mu.Lock()
				defer e.Mu.Unlock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil { t.Fatal(err) }
				environment, err := e.PreparedEnvironmentLocked(auth, true)
				if err != nil { t.Fatal(err) }
				if err := publication.Accept(context.Background(), auth.Catalog, environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed { t.Fatal("retired owner accepted publication", err) }
			}()
			if owner.Current().Generation != retired.Generation { t.Fatal("rejection mutated retired owner generation") }
			assertRetained(retired.Generation)
			parse()
			if registry.Lookup("seek_recipe").Description.Folds == owner || registry.Lookup("seek_recipe").Description.Analysis == analysis { t.Fatal("reparse adopted retired owner") }
			assertRetained(retired.Generation)
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("seek_first") })
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("seek_second") })
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
