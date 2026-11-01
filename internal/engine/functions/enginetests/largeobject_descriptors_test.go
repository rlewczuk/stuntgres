package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestLargeObjectDescriptorCall(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		call string
		oid ids.OID
		result sqltype.ID
		formals []sqltype.ID
	}{
		{"lo_open(NULL,131072)", 952, sqltype.Int4, []sqltype.ID{sqltype.Oid, sqltype.Int4}},
		{"lo_close(NULL)", 953, sqltype.Int4, []sqltype.ID{sqltype.Int4}},
		{"loread(NULL,2)", 954, sqltype.Bytea, []sqltype.ID{sqltype.Int4, sqltype.Int4}},
		{"lowrite(NULL,decode('00ff','hex'))", 955, sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Bytea}},
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.call)
		n := node.(functions.LargeObjectCall)
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || d.Address().ObjectID != tc.oid || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 'u' { t.Fatal("descriptor provider identity", d, err) }
		if n.Usage.Type.ID() != tc.result || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren { t.Fatal("typed nonfolding recipe", n) }
		for i, child := range n.Args { if child.ResultUsage().Type.ID() != tc.formals[i] { t.Fatal("signed formal carrier", i, child.ResultUsage()) } }
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("strict typed NULL", value, err) }
		children := append([]expr.Expr(nil), n.Args...)
		rebuilt, err := n.Rebuild(n.Info, children)
		if err != nil { t.Fatal(err) }
		copy := rebuilt.(functions.LargeObjectCall)
		children[0] = nil
		if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.RoutineReference != n.RoutineReference { t.Fatal("rebuild borrowed argument vector or lost provenance") }
	}
	// Real volatile children distinguish once-only evaluation from NULL-based
	// skipping or replay. No mock runtime or echo provider supplies this witness.
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "lo_open(NULL,nextval('strict_null_steps'::regclass)::integer)")
	for iteration := int64(1); iteration <= 2; iteration++ {
		value, err := node.Eval(&evaluation)
		if err != nil || !value.IsNull() { t.Fatal(value, err) }
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err = current.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		n, err := value.Integer()
		if err != nil || n != iteration { t.Fatal("argument demand repeated or suppressed", n, err) }
	}
	// Bypass planning deliberately: a reached provider must demand arithmetic
	// even with a runtime NULL, independently of shared planning elimination.
	runtimeNull, _, runtimeContext := strictNullUnitBind(t, e, op, scope, "loread(NULL,1/0)")
	_, runtimeErr := runtimeNull.Eval(&runtimeContext)
	var runtimeDiagnostic *sqlstate.Error
	if !errors.As(runtimeErr, &runtimeDiagnostic) || runtimeDiagnostic.Code != "22012" || runtimeDiagnostic.Message != "division by zero" { t.Fatal("runtime NULL erased sibling error", runtimeErr) }
	invalid, _, ctx := strictNullUnitBind(t, e, op, scope, "loread(-1,0)")
	_, err := invalid.Eval(&ctx)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != "invalid large-object descriptor: -1" { t.Fatal("descriptor coerced through OID", err) }
	// Runtime result carriers use the actual transaction table, not a synthetic
	// capability. Bind/Describe above has opened no slots; the first is still 0.
	_, err = core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "lo_from_bytea", OID: 910291, Data: "abcdef"})
	if err != nil { t.Fatal(err) }
	open, _, ctx := strictNullUnitBind(t, e, op, scope, "lo_open(910291::oid,131072)")
	before := open.(functions.LargeObjectCall)
	value, err := open.Eval(&ctx)
	if err != nil { t.Fatal(err) }
	fd, err := value.Integer()
	if err != nil || fd != 0 || value.Type().ID() != sqltype.Int4 { t.Fatal("INT4 open result", value, err) }
	write, _, writeContext := strictNullUnitBind(t, e, op, scope, "lowrite(0,decode('00ff','hex'))")
	value, err = write.Eval(&writeContext)
	if err != nil { t.Fatal(err) }
	count, err := value.Integer()
	if err != nil || count != 2 || value.Type().ID() != sqltype.Int4 { t.Fatal("INT4 write result", value, err) }
	read, _, readContext := strictNullUnitBind(t, e, op, scope, "loread(0,2)")
	value, err = read.Eval(&readContext)
	if err != nil { t.Fatal(err) }
	data, err := value.Binary()
	if err != nil || data != "cd" || value.Type().ID() != sqltype.Bytea { t.Fatal("BYTEA descriptor result", value, err) }
	if !reflect.DeepEqual(before, open) { t.Fatal("recipe retained runtime cursor or transaction authority") }
	original := scope.TxRef
	scope.TxRef = original.Domain().Tx(original.Session(), original.ID(), original.Epoch()+1, original.Attachment(), original.Diagnostic())
	_, fenceErr := core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "loread", Descriptor: 0, Length: 1})
	scope.TxRef = original
	if !errors.Is(fenceErr, ids.ErrCatalogReplaced) { t.Fatal("old descriptor crossed catalog epoch", fenceErr) }
}

func TestLargeObjectDescriptorArgumentDemand(t *testing.T) {
	for _, planner := range []struct {
		name string
		run func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			for _, text := range []string{
				"lo_open($1::oid,1/$2::integer)",
				"loread($1::integer,1/$2::integer)",
				"lowrite(1/$2::integer,$1::bytea)",
				"lo_get($1::oid,0,1/$2::integer)",
				"lo_put($1::oid,1::bigint/$2::bigint,NULL::bytea)",
			} {
				t.Run(text, func(t *testing.T) {
					e, op, scope := strictNullUnitFixture(t)
					node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
					zero, err := sqltype.Integer(binder.Params.Parameters[2].Type, 0)
					if err != nil { t.Fatal(err) }
					evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type), zero}
					planned, _, err := planner.run(&evaluation, node, true)
					if err == nil {
						_, err = planned.Eval(&evaluation)
					}
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
						t.Fatal("strict NULL suppressed demanded child error", err)
					}
				})
			}
			for _, text := range []string{
				"lo_open(NULL,nextval('strict_null_steps'::regclass)::integer)",
				"loread(NULL,nextval('strict_null_steps'::regclass)::integer)",
				"lowrite(nextval('strict_null_steps'::regclass)::integer,NULL::bytea)",
				"lo_get(NULL,nextval('strict_null_steps'::regclass),1)",
				"lo_put(NULL,nextval('strict_null_steps'::regclass),NULL::bytea)",
			} {
				t.Run(text, func(t *testing.T) {
					e, op, scope := strictNullUnitFixture(t)
					node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
					sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
					planned, _, err := planner.run(&evaluation, node, true)
					if err != nil { t.Fatal(err) }
					for range 2 {
						value, err := planned.Eval(&evaluation)
						if err != nil || !value.IsNull() || value.Type().ID() != node.ResultUsage().Type.ID() { t.Fatal("typed strict result", value, err) }
						e.Mu.Lock()
						called := e.Storage.Sequences[sequence.Address].Called
						e.Mu.Unlock()
						if called { t.Fatal("known NULL executed volatile sibling") }
					}
				})
			}
		})
	}
}

func TestLargeObjectTruncateCall(t *testing.T) {
	t.Run("typed-recipe", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		replacement := make(map[sqltype.ID]*sqltype.Descriptor)
		for _, d := range sqltype.Builtins() { replacement[d.ID()] = d }
		for _, tc := range []struct { name string; oid ids.OID; length sqltype.ID }{
			{"lo_truncate", 1004, sqltype.Int4}, {"lo_truncate64", 3172, sqltype.Int8},
		} {
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.name+"(NULL,-1)")
			n := node.(functions.LargeObjectCall)
			declaration, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || declaration == nil || declaration.Address().ObjectID != tc.oid || !declaration.Strict || declaration.Volatility != expr.Volatile || declaration.Builtin.Parallel != 'u' { t.Fatal("truncate identity", declaration, err) }
			if n.Usage.Type.ID() != sqltype.Int4 || n.Args[0].ResultUsage().Type.ID() != sqltype.Int4 || n.Args[1].ResultUsage().Type.ID() != tc.length || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren { t.Fatal("signed formals or nonfolding policy lost", n) }
			value, err := n.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("typed strict NULL", value, err) }
			operands, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || operands.Count != 2 { t.Fatal("capture shape", operands, err) }
			for i, want := range n.Args {
				got, err := n.CaptureOperand(i)
				if err != nil || !reflect.DeepEqual(got, want) { t.Fatal("capture operand", i, err) }
			}
			if _, err := n.CaptureOperand(2); err == nil { t.Fatal("accepted nonexistent operand") }
			rendered, err := n.Render()
			if err != nil || rendered.Name != tc.name || rendered.Form != expr.FormCall || len(rendered.Inputs) != 2 { t.Fatal("render shape", rendered, err) }
			children := append([]expr.Expr(nil), n.Args...)
			rebuilt, err := n.Rebuild(n.Info, children)
			if err != nil { t.Fatal(err) }
			copy := rebuilt.(functions.LargeObjectCall)
			children[0] = nil
			if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.RoutineReference != n.RoutineReference { t.Fatal("rebuild borrowed operands or lost provider") }
			if _, err := n.Rebuild(n.Info, nil); err == nil { t.Fatal("accepted truncated operand vector") }
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
			if err != nil || !changed { t.Fatal("retag", changed, err) }
			next := retagged.(functions.LargeObjectCall)
			if next.Usage.Type != replacement[sqltype.Int4] || next.Args[0].ResultUsage().Type != replacement[sqltype.Int4] || next.Args[1].ResultUsage().Type != replacement[tc.length] || next.RoutineReference != n.RoutineReference || &next.Args[0] == &n.Args[0] { t.Fatal("retag lost independent recipe or provider") }
			value, err = next.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != replacement[sqltype.Int4] || n.Usage.Type == replacement[sqltype.Int4] { t.Fatal("retag mutated original or lost result type", value, err) }
			invalid, _, ctx := strictNullUnitBind(t, e, op, scope, tc.name+"(-1,-1)")
			_, err = invalid.Eval(&ctx)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "42704" || d.Message != "invalid large-object descriptor: -1" { t.Fatal("FD extracted as OID", err) }
		}
		_, err := core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name:"lo_from_bytea", OID:910292, Data:"abcdef"})
		if err != nil { t.Fatal(err) }
		open, _, ctx := strictNullUnitBind(t, e, op, scope, "lo_open(910292::oid,131072)")
		value, err := open.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		if fd, err := value.Integer(); err != nil || fd != 0 { t.Fatal("binding or NULL evaluation allocated a handle", value, err) }
		for _, text := range []string{"lo_truncate(0,2)", "lo_truncate64(0,2147483648::bigint)", "lo_truncate64(0,4398046509056::bigint)"} {
			node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
			before := node.(functions.LargeObjectCall)
			for range 2 {
				value, err := node.Eval(&ctx)
				if err != nil { t.Fatal(err) }
				result, err := value.Integer()
				if err != nil || result != 0 || value.Type().ID() != sqltype.Int4 { t.Fatal("INT4 truncate result", value, err) }
				if !reflect.DeepEqual(before, node) { t.Fatal("recipe retained runtime authority") }
			}
		}
		original := scope.TxRef
		scope.TxRef = original.Domain().Tx(original.Session(), original.ID(), original.Epoch()+1, original.Attachment(), original.Diagnostic())
		_, fenceErr := core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name:"lo_truncate64", Descriptor:0, Length:1})
		scope.TxRef = original
		if !errors.Is(fenceErr, ids.ErrCatalogReplaced) { t.Fatal("old descriptor crossed epoch", fenceErr) }
	})
	t.Run("runtime-demand", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		iteration := int64(0)
		for _, name := range []string{"lo_truncate", "lo_truncate64"} {
			for _, text := range []string{
				name+"(NULL,nextval('strict_null_steps'::regclass)::integer)",
				name+"(nextval('strict_null_steps'::regclass)::integer,NULL)",
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
			for _, text := range []string{name+"(NULL,1/0)", name+"(1/0,NULL)"} {
				node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
				_, err := node.Eval(&ctx)
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22012" || d.Message != "division by zero" { t.Fatal("runtime NULL skipped child error", err) }
			}
		}
	})
	for _, planner := range []struct {
		name string
		run func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			for _, name := range []string{"lo_truncate", "lo_truncate64"} {
				for _, text := range []string{name+"(NULL,nextval('strict_null_steps'::regclass)::integer)", name+"(nextval('strict_null_steps'::regclass)::integer,NULL)"} {
					e, op, scope := strictNullUnitFixture(t)
					node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
					sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
					planned, _, err := planner.run(&ctx, node, true)
					if err != nil { t.Fatal(err) }
					for range 2 {
						value, err := planned.Eval(&ctx)
						if err != nil || !value.IsNull() || value.Type().ID() != sqltype.Int4 { t.Fatal("planning NULL type", value, err) }
					}
					e.Mu.Lock()
					called := e.Storage.Sequences[sequence.Address].Called
					e.Mu.Unlock()
					if called { t.Fatal("planning-known NULL executed sibling") }
				}
				for _, text := range []string{name+"($1::integer,1/$2::integer)", name+"(1/$2::integer,$1::integer)"} {
					e, op, scope := strictNullUnitFixture(t)
					node, binder, ctx := strictNullUnitBind(t, e, op, scope, text)
					zero, err := sqltype.Integer(binder.Params.Parameters[2].Type, 0)
					if err != nil { t.Fatal(err) }
					ctx.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type), zero}
					planned, _, err := planner.run(&ctx, node, true)
					if err == nil { _, err = planned.Eval(&ctx) }
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "22012" || d.Message != "division by zero" { t.Fatal("planning NULL erased demanded child error", err) }
				}
			}
		})
	}
	t.Run("definition-ownership", truncateRecipeOwnership)
}

func truncateRecipeOwnership(t *testing.T) {
	for _, tc := range []struct { name string; oid ids.OID }{{"lo_truncate",1004}, {"lo_truncate64",3172}} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			parse := func() {
				command(func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("truncate_recipe", "SELECT "+tc.name+"(0,COALESCE(-(7::integer),0))", nil)
					return err
				})
			}
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "truncate_recipe", &enginetest.ProtocolReader{Data:[]byte{0,0,0,0,0,0}})
					return err
				})
			}
			parse()
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("truncate_recipe")
			if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil { t.Fatal("missing genuine definition") }
			owner := definition.Description.Folds
			if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 { t.Fatal("Parse evaluated operands") }
			command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolStatement("truncate_recipe"); return err })
			if definition.Description.Folds != owner || owner.Current().Generation != 0 { t.Fatal("Describe replaced owner or evaluated operands") }
			bind("truncate_first")
			first, accepted := registry.Binding("truncate_first"), owner.Current()
			if first == nil || first.Resource == nil || first.Resource.Root == nil || first.Folds == nil || len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("Bind lost accepted owner/generation/backing") }
			root := first.Resource.Root
			if len(root.Expressions) != 1 { t.Fatal("missing scalar recipe") }
			node, ok := root.Expressions[0].(functions.LargeObjectCall)
			if !ok || node.Name != tc.name || node.PlanningFold() != expr.FoldNever { t.Fatal("truncate folded or replaced") }
			declaration, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || declaration == nil || declaration.Address().ObjectID != tc.oid { t.Fatal("provider incarnation lost", err) }
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), declaration); err != nil { t.Fatal(err) }
			if len(accepted.Dependencies) == 0 { t.Fatal("accepted provider dependencies absent") }
			for _, dependency := range accepted.Dependencies {
				if dependency.Ref.Epoch != e.Catalog.State.Epoch() { t.Fatal("accepted provider crossed incarnation") }
			}
			for _, entry := range accepted.Entries {
				if entry.Callable != nil && ids.OID(entry.Callable.RoutineOID()) == tc.oid { t.Fatal("volatile runtime result retained") }
			}
			child := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "-7")
			if child.Source == (expr.FoldSourceOccurrence{}) || child.Frame != child.Source.Frame || len(child.Inputs) == 0 { t.Fatal("child lost provenance/inputs", child) }
			provider, ok := child.Callable.(*catalog.Procedure)
			if !ok || provider.Volatility != expr.Immutable { t.Fatal("child lost immutable provider") }
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), provider); err != nil { t.Fatal(err) }
			found := false
			for _, input := range child.Inputs {
				if input.Value.Type() != nil {
					if n, err := input.Value.Integer(); err == nil && n == 7 { found = true }
				}
			}
			if !found { t.Fatal("child lost original input") }
			index := -1
			for i, entry := range accepted.Entries { if enginetest.SamePublishedFold(entry, child) { index = i; break } }
			if index < 0 { t.Fatal("child absent from accepted generation") }
			bind("truncate_second")
			second := registry.Binding("truncate_second")
			if second == first || second.Resource == first.Resource || second.Resource.Root == root || second.Folds.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(second.Folds.Entries[index], child) || &second.Folds.Entries[index].Inputs[0] != &accepted.Entries[index].Inputs[0] { t.Fatal("independent binding lost graph separation or immutable backing") }
			publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, owner.Environment, e.CatalogRead(), nil)
			if err != nil || publication == nil { t.Fatal("missing actual publication attempt", err) }
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("truncate_recipe") })
			if owner.Live() || first.Resource.Root != root || first.Folds.Generation != accepted.Generation { t.Fatal("retirement mutated accepted graph") }
			retired := owner.Current()
			if err := publication.Accept(context.Background(), e.CatalogRead(), owner.Environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("retired definition accepted publication", err) }
			if owner.Current().Generation != retired.Generation || len(owner.Current().Entries) != len(retired.Entries) || &first.Folds.Entries[0] != &accepted.Entries[0] || &second.Folds.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(first.Folds.Entries[index], child) { t.Fatal("rejected publication changed retained ownership") }
			parse()
			if registry.Lookup("truncate_recipe").Description.Folds == owner { t.Fatal("new definition adopted retired owner") }
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("truncate_first") })
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("truncate_second") })
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
