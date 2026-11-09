package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBStripNullsAcceptedOwner(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	const source = `SELECT jsonb_strip_nulls('{"a":null,"b":[null,1]}'::jsonb,true) AS stripped`
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("strip_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("strip_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing registered strip definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse executed strip or lost definition ownership")
	}
	view := e.CatalogRead()
	routine := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3262})
	if routine.Builtin == nil || routine.Builtin.Providers != functions.JSONRoutineProvider {
		t.Fatal("strip is not backed by executable JSON provider authority")
	}
	// Reconstruct a real analyzed call, retaining both exact typed children and
	// its own occurrence. A separately bound graph need not have the accepted
	// graph's expansion ordinal.
	op := enginetest.TransactionOperation(t, s)
	query := enginetest.BindSelect(t, e, op, source)
	op.Done()
	original := query.Root.(*plan.ProjectNode).Expressions[0].(functions.JSONStripNulls)
	if original.RoutineReference != routine || original.Plan.Name() != "jsonb_strip_nulls" || original.Usage.Type.ID() != sqltype.JSONB || original.Source == (expr.FoldSourceOccurrence{}) || original.Occurrence == 0 {
		t.Fatal("analyzed strip lost provider or provenance", original)
	}
	rebuiltExpr, err := original.Rebuild(original.Info, original.Children())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := rebuiltExpr.(functions.JSONStripNulls)
	if rebuilt.Info != original.Info || rebuilt.Plan != original.Plan || !reflect.DeepEqual(rebuilt.Args, original.Args) || &rebuilt.Args[0] == &original.Args[0] {
		t.Fatal("reconstruction lost exact provenance or typed operands")
	}
	for i, id := range []sqltype.ID{sqltype.JSONB, sqltype.Bool} {
		if rebuilt.Args[i].ResultUsage().Type.ID() != id {
			t.Fatal("reconstruction lost formal type", i)
		}
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "strip_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := registry.Binding(name)
		if binding == nil || binding.Entry.Description.Folds != owner || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil {
			t.Fatal("missing actual accepted strip binding")
		}
		return binding
	}
	first := bind("strip_first")
	accepted := owner.Current()
	entry := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.JSONB, `{"b": [1]}`)
	if accepted.Generation == 0 || entry.Callable != routine || entry.Result.Type.ID() != sqltype.JSONB || entry.Value.Type() != entry.Result.Type || entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || len(entry.Inputs) != 2 {
		t.Fatal("accepted strip lost exact routine/result/provenance/inputs", entry)
	}
	for i, want := range []struct {
		id   sqltype.ID
		text string
	}{{sqltype.JSONB, `{"a": null, "b": [null, 1]}`}, {sqltype.Bool, "t"}} {
		input := entry.Inputs[i]
		if input.Usage.Type.ID() != want.id || input.Value.Type() != input.Usage.Type || input.Value.IsNull() {
			t.Fatal("accepted input lost original type authority", i, input)
		}
		text, err := input.Usage.Type.Output(context.Background(), input.Value)
		if err != nil || text != want.text {
			t.Fatal("accepted input is not original operand", i, text, err)
		}
	}
	if len(accepted.Dependencies) == 0 || definition.Description.Analysis.FoldCallables[routine.Address()] != routine {
		t.Fatal("accepted fold lost captured provider authority")
	}
	for _, address := range []ids.ObjectAddress{routine.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.JSONB)}, {ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)}} {
		found := false
		for _, dependency := range accepted.Dependencies {
			descriptor := view.Get(address)
			if descriptor != nil && dependency.Ref == e.Catalog.Domain.Ref(view.Epoch(), address) && dependency.Descriptor == descriptor && dependency.Stamp == descriptor.Revision() {
				found = true
			}
		}
		if !found {
			t.Fatal("accepted strip lost exact routine/type dependency", address)
		}
	}
	assertGraph := func(root *plan.ProjectNode, saved *plan.LiteralExpr) plan.LiteralExpr {
		t.Helper()
		if root == nil || len(root.Expressions) != 1 {
			t.Fatal("missing retained private strip graph")
		}
		literal, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || literal.ResultUsage() != entry.Result || !sqltype.SameDatum(literal.Value, entry.Value) || literal.Source != entry.Source || literal.Occurrence != entry.Occurrence || literal.Pos.Offset != entry.Offset || saved != nil && literal.Info != saved.Info {
			t.Fatal("retained graph lost exact accepted datum/provenance")
		}
		return literal
	}
	firstRoot, firstFolds := first.Resource.Root, *first.Folds
	firstLiteral := assertGraph(firstRoot, nil)
	second := bind("strip_second")
	secondRoot, secondFolds := second.Resource.Root, *second.Folds
	secondLiteral := assertGraph(secondRoot, nil)
	if first == second || first.Resource == second.Resource || firstRoot == secondRoot || &firstRoot.Expressions[0] == &secondRoot.Expressions[0] {
		t.Fatal("independent bindings shared mutable executable resources")
	}
	assertRetained := func() {
		t.Helper()
		current := owner.Current()
		if registry.Lookup("strip_owned") != definition || definition.Description.Folds != owner || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || &current.Dependencies[0] != &accepted.Dependencies[0] {
			t.Fatal("publication changed actual owner/generation/backing")
		}
		for _, folds := range []exec.AcceptedFolds{firstFolds, secondFolds} {
			if folds.Generation != accepted.Generation || &folds.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(entry, folds.Entries[0]) || &folds.Entries[0].Inputs[0] != &entry.Inputs[0] || &folds.Entries[0].Inputs[1] != &entry.Inputs[1] {
				t.Fatal("retained accepted group lost generation, datum or input backing")
			}
		}
		assertGraph(firstRoot, &firstLiteral)
		assertGraph(secondRoot, &secondLiteral)
	}
	assertRetained()
	func() {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal("missing real strip publication", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("canceled publication accepted candidate", err)
		}
	}()
	assertRetained()
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("strip_rejected", `SELECT jsonb_strip_nulls('{"a":null}'::jsonb),(1/0)::bigint`, nil)
		return err
	})
	bad := registry.Lookup("strip_rejected")
	if bad == nil || bad.Description.Folds == nil || bad.Description.Folds.Current().Generation != 0 || len(bad.Description.Folds.Current().Entries) != 0 {
		t.Fatal("Parse published unexecuted sibling folds")
	}
	err = enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("strip_failed", "strip_rejected", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
		t.Fatal("missing genuine demanded sibling planning failure", err)
	}
	if registry.Binding("strip_failed") != nil || bad.Description.Folds.Current().Generation != 0 || len(bad.Description.Folds.Current().Entries) != 0 {
		t.Fatal("failed sibling published partial fold group or binding")
	}
	// Saved immutable groups and graph roots outlive terminal cleanup of any
	// live producer released by the failed command.
	assertRetained()
}

func TestJSONBStripNullsParameterizedOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("strip_parameters", `SELECT jsonb_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped`, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("strip_parameters")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Parameters.Count != 2 {
		t.Fatal("missing real parameterized definition")
	}
	owner := definition.Description.Folds
	routine := e.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3262})
	assertOwner := func() {
		t.Helper()
		if registry.Lookup("strip_parameters") != definition || definition.Description.Folds != owner || definition.Description.Analysis.FoldCallables[routine.Address()] != routine || owner != nil && (owner.Current().Generation != 0 || len(owner.Current().Entries) != 0) {
			t.Fatal("parameter-dependent strip persisted a definition fold")
		}
	}
	wire := func(target, flag *string) []byte {
		data := []byte{0, 0, 0, 2}
		for _, value := range []*string{target, flag} {
			if value == nil {
				data = binary.BigEndian.AppendUint32(data, ^uint32(0))
			} else {
				data = binary.BigEndian.AppendUint32(data, uint32(len(*value)))
				data = append(data, (*value)...)
			}
		}
		return append(data, 0, 0)
	}
	str := func(s string) *string { return &s }
	var previous *core.PreparedBinding
	for i, tc := range []struct {
		target, flag *string
		want         string
		null         bool
		code         string
	}{
		{str(`{"a":null,"b":[null,1]}`), str("true"), `{"b": [1]}`, false, ""},
		{str(`{"a":null,"b":[null,2]}`), str("false"), `{"b": [null, 2]}`, false, ""},
		{str(`{"a":null}`), nil, "", true, ""},
		{nil, str("true"), "", true, ""},
		{str(`{"a":null}`), str("truth"), "", false, "22P02"},
		{str(`[null,4]`), str("true"), `[4]`, false, ""},
		{nil, str("truth"), "", false, "22P02"},
		{str(`[null,5]`), str("false"), `[null, 5]`, false, ""},
		{str(`oops`), nil, "", false, "22P02"},
		{str(`[null,3]`), str("true"), `[3]`, false, ""},
	} {
		name := fmt.Sprintf("strip_parameter_%d", i)
		err := enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "strip_parameters", &enginetest.ProtocolReader{Data: wire(tc.target, tc.flag)})
			return err
		})
		if tc.code != "" {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || registry.Binding(name) != nil {
				t.Fatal("rejected conversion published binding", err)
			}
			assertOwner()
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		binding := registry.Binding(name)
		if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil || binding.Entry.Description.Analysis == nil || previous != nil && (binding.Resource == previous.Resource || binding.Resource.Root == previous.Resource.Root) {
			t.Fatal("parameterized binding lost independent resources")
		}
		for _, entry := range binding.Folds.Entries {
			if entry.Callable != nil && entry.Callable.RoutineOID() == 3262 {
				t.Fatal("parameter-dependent call became persistent literal fold")
			}
		}
		rows := 0
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func(fields []session.ProtocolField) error {
				rows++
				if len(fields) != 1 || fields[0].Null != tc.null || !tc.null && string(fields[0].Bytes) != tc.want {
					t.Fatal("parameter binding reused stale stripped datum", fields, tc)
				}
				return nil
			}, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		if rows != 1 {
			t.Fatal("missing actual parameter execution", rows)
		}
		previous = binding
		assertOwner()
	}
}
