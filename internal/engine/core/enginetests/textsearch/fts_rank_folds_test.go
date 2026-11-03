package textsearch_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestFTSRankPreparedOwnership(t *testing.T) {
	t.Run("typed_accepted_generation", func(t *testing.T) {
		for _, tc := range []struct {
			query, want string
			bits        uint32
		}{
			{"SELECT ts_rank('cat:1'::tsvector,'cat'::tsquery)", "0.06079271", 0x3d7901c7},
			{"SELECT ts_rank_cd('cat:1'::tsvector,'cat'::tsquery)", "0.1", 0x3dcccccd},
		} {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			ast, d := enginetest.PreparedDescription(t, e, s, tc.query)
			if d.Folds == nil || len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
				t.Fatal("Describe evaluated rank")
			}
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			entry := enginetest.PreparedFoldEntry(t, d, sqltype.Float4, tc.want)
			bits, err := entry.Value.Float4Bits()
			if err != nil || bits != tc.bits {
				t.Fatal("accepted REAL bits", bits, err)
			}
			before := enginetest.SnapshotFoldDescription(d)
			enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			after := d.Folds.Current()
			if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, sqltype.Float4, tc.want)) {
				t.Fatal("rank accepted artifact replaced")
			}
			enginetest.ConvertRejectPublication(t, e, s, ast, d, before.Accepted)
			ftsRankHeadlineRejectStale(t, e, s, ast, d, before.Accepted)
		}
	})
	t.Run("binding_graphs_and_frames", func(t *testing.T) {
		ftsRankHeadlinePortals(t, "SELECT ts_rank('cat:1'::tsvector,'cat'::tsquery),ts_rank($1::tsvector,'cat'::tsquery)", "cat:1", "dog:1",
			[][]byte{{0x3d, 0x79, 0x01, 0xc7}, {0x3d, 0x79, 0x01, 0xc7}}, [][]byte{{0x3d, 0x79, 0x01, 0xc7}, {0, 0, 0, 0}}, true, 0, true)
	})
}

// Exercise actual prepared definitions and two binding-local graph/frame owners.
// Closing the definition must not retire either already-admitted portal.
func ftsRankHeadlinePortals(t *testing.T, query, oldInput, newInput string, oldWant, newWant [][]byte, binary bool, wantCollation uint32, wantFold bool) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("rank_headline_definition", query, nil)
		return err
	})
	bind := func(name, input string) {
		command(func(scope *core.StatementScope) error {
			data := []byte{0, 0, 0, 1, 0, 0, 0, byte(len(input))}
			data = append(data, []byte(input)...)
			if binary {
				data = append(data, 0, 1, 0, 1)
			} else {
				data = append(data, 0, 0)
			}
			_, err := scope.BindProtocolPortal(name, "rank_headline_definition", &enginetest.ProtocolReader{Data: data})
			return err
		})
	}
	bind("rank_headline_old", oldInput)
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("rank_headline_definition")
	if owner := definition.Description.Folds; owner != nil && len(owner.Current().Entries) != 0 {
		t.Fatal("binding frame entered persistent definition")
	}
	old := registry.Binding("rank_headline_old")
	if old == nil || old.Resource == nil || old.Resource.Root == nil || old.Folds == nil {
		t.Fatal("missing binding artifacts")
	}
	oldRoot, oldResource, accepted := old.Resource.Root, old.Resource, *old.Folds
	oldFolds := old.Folds
	var oldMetadata []expr.Info
	if !wantFold {
		oldMetadata = make([]expr.Info, len(oldRoot.Expressions))
		for i, expression := range oldRoot.Expressions {
			oldMetadata[i] = expr.InfoOf(expression)
		}
	}
	if wantFold != (len(accepted.Entries) != 0) || !wantFold && (accepted.Generation != 0 || len(accepted.Dependencies) != 0) {
		t.Fatal("unexpected binding fold admission", accepted)
	}
	checkMetadata := func(root *plan.ProjectNode, want [][]byte) {
		t.Helper()
		if len(root.Schema()) != len(want) || len(root.Expressions) != len(want) {
			t.Fatal("portal lost retained result shape")
		}
		for i, column := range root.Schema() {
			usage := definition.Description.Results[i].Usage
			if column.Usage != usage || usage.Collation.ID != wantCollation || root.Expressions[i].ResultUsage() != usage {
				t.Fatal("portal lost definition collation/descriptor", i, column)
			}
			position := root.Expressions[i].ExplicitCollation()
			if wantCollation != 0 && wantCollation != 100 {
				wantPosition := strings.Index(query, "COLLATE") + 1
				if i == 1 {
					wantPosition = strings.LastIndex(query, "COLLATE") + 1
				}
				if position.Character != wantPosition {
					t.Fatal("portal lost explicit collation provenance", i, position)
				}
			}
			if !wantFold {
				if usage.Type.ID() != sqltype.Text || usage.Typmod != -1 {
					t.Fatal("collated portal lost canonical TEXT usage", i)
				}
				// Immutable preflight computes this binding-local value, but
				// nonliteral/parameter children prevent persistent capture.
				// The value is not a callable: preflight clears that reference.
				literal, ok := root.Expressions[i].(plan.LiteralExpr)
				if !ok || literal.RoutineReference != nil || literal.Value.IsNull() || literal.Value.Type() != usage.Type || !literal.Value.ValidResult(usage) {
					t.Fatalf("collated portal lost typed literal authority: slot %d, %T", i, root.Expressions[i])
				}
				text, err := literal.Value.Text()
				if err != nil || text != string(want[i]) {
					t.Fatal("collated portal literal bytes", i, text, err)
				}
			}
		}
	}
	checkMetadata(oldRoot, oldWant)
	var oldText []string
	if !wantFold {
		oldText = make([]string, len(oldRoot.Expressions))
		for i, expression := range oldRoot.Expressions {
			oldText[i], _ = expression.(plan.LiteralExpr).Value.Text()
		}
	}
	wantOID, wantArity := uint32(3706), 2
	if !binary {
		wantOID, wantArity = 3744, 3
	}
	view := e.CatalogRead()
	procedure := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(wantOID)})
	checkRoutine := func(binding *core.PreparedBinding) {
		t.Helper()
		analysis := binding.Entry.Description.Analysis
		if analysis == nil || analysis.FoldCallables[procedure.Address()] != procedure {
			t.Fatal("collated binding lost original analyzed routine authority")
		}
	}
	if !wantFold {
		checkRoutine(old)
	}
	wantProvider := functions.FTSRankRoutineProvider
	wantInputs := []sqltype.ID{sqltype.TSVector, sqltype.TSQuery}
	if !binary {
		wantProvider = functions.FTSHeadlineRoutineProvider
		wantInputs = []sqltype.ID{sqltype.Regconfig, sqltype.Text, sqltype.TSQuery}
	}
	selectedFold := func(entries []exec.FoldConstant) exec.FoldConstant {
		for _, entry := range entries {
			if entry.Callable != nil && entry.Callable.RoutineOID() == wantOID {
				selected, ok := entry.Callable.(*catalog.Procedure)
				if !ok || selected != procedure || selected.Builtin == nil || selected.Builtin.Providers != wantProvider || entry.Occurrence == 0 || len(entry.Inputs) != wantArity || entry.Operator != (ids.ObjectRef{}) {
					t.Fatal("selected fold lost original declaration/provider/occurrence/ordered inputs", entry)
				}
				if entry.Result.Collation.ID != wantCollation || entry.Value.Type() != entry.Result.Type {
					t.Fatal("selected fold lost collated result authority", entry)
				}
				for i, input := range entry.Inputs {
					formal, ok := view.LookupType(wantInputs[i])
					if !ok || input.Usage.Type != formal || input.Value.Type() != formal {
						t.Fatal("selected fold lost ordered formal/datum generation", i)
					}
				}
				return entry
			}
		}
		t.Fatal("binding omitted selected routine fold", wantOID)
		return exec.FoldConstant{}
	}
	var oldCall exec.FoldConstant
	if wantFold {
		oldCall = selectedFold(accepted.Entries)
	}
	bind("rank_headline_new", newInput)
	fresh := registry.Binding("rank_headline_new")
	if fresh == nil || fresh.Resource == nil || fresh.Resource.Root == nil || fresh.Resource == oldResource || fresh.Resource.Root == oldRoot || fresh.Folds == nil || fresh.Folds == oldFolds {
		t.Fatal("bindings shared graph or frame")
	}
	freshRoot, freshResource, freshFolds := fresh.Resource.Root, fresh.Resource, fresh.Folds
	freshAccepted := *freshFolds
	checkMetadata(freshRoot, newWant)
	var freshText []string
	if !wantFold {
		checkRoutine(fresh)
		freshText = make([]string, len(freshRoot.Expressions))
		for i, expression := range freshRoot.Expressions {
			freshText[i], _ = expression.(plan.LiteralExpr).Value.Text()
		}
	}
	if wantFold {
		if len(fresh.Folds.Entries) == 0 || &fresh.Folds.Entries[0] == &accepted.Entries[0] {
			t.Fatal("bindings shared mutable fold backing")
		}
		freshCall := selectedFold(fresh.Folds.Entries)
		if freshCall.Callable != oldCall.Callable || &freshCall.Inputs[0] == &oldCall.Inputs[0] {
			t.Fatal("binding lost original routine authority or shared mutable input frame")
		}
	} else if fresh.Folds.Generation != accepted.Generation || len(fresh.Folds.Entries) != 0 || len(fresh.Folds.Dependencies) != 0 {
		t.Fatal("collated binding invented accepted fold payload", *fresh.Folds)
	}
	var freshMetadata []expr.Info
	if !wantFold {
		freshMetadata = make([]expr.Info, len(freshRoot.Expressions))
		for i, expression := range freshRoot.Expressions {
			freshMetadata[i] = expr.InfoOf(expression)
		}
	}
	checkRetained := func() {
		t.Helper()
		checkMetadata(oldRoot, oldWant)
		checkMetadata(freshRoot, newWant)
		if !wantFold {
			checkRoutine(old)
			checkRoutine(fresh)
			if len(oldRoot.Expressions) != len(oldMetadata) || len(freshRoot.Expressions) != len(freshMetadata) {
				t.Fatal("collated portal changed retained graph shape")
			}
			for i := range oldMetadata {
				if expr.InfoOf(oldRoot.Expressions[i]) != oldMetadata[i] || expr.InfoOf(freshRoot.Expressions[i]) != freshMetadata[i] {
					t.Fatal("collated portal mutated accepted expression metadata", i)
				}
				left, _ := oldRoot.Expressions[i].(plan.LiteralExpr).Value.Text()
				right, _ := freshRoot.Expressions[i].(plan.LiteralExpr).Value.Text()
				if unsafe.StringData(left) != unsafe.StringData(oldText[i]) || unsafe.StringData(right) != unsafe.StringData(freshText[i]) {
					t.Fatal("collated portal replaced owned literal backing", i)
				}
			}
		}
	}
	checkRetained()
	command(func(scope *core.StatementScope) error {
		return scope.CloseProtocolStatement("rank_headline_definition")
	})
	checkRetained()
	for _, tc := range []struct {
		name string
		want [][]byte
	}{{"rank_headline_old", oldWant}, {"rank_headline_new", newWant}} {
		rows := 0
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(tc.name, 0, func(fields []session.ProtocolField) error {
				rows++
				if len(fields) != len(tc.want) {
					t.Fatal("portal shape", tc.name, fields)
				}
				for i := range fields {
					if fields[i].Null || !bytes.Equal(fields[i].Bytes, tc.want[i]) {
						t.Fatal("portal result", tc.name, i, fields[i], tc.want[i])
					}
				}
				return nil
			}, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		if rows != 1 {
			t.Fatal("portal cardinality", tc.name, rows)
		}
		checkRetained()
	}
	if accepted.Generation != oldFolds.Generation || len(accepted.Entries) != len(oldFolds.Entries) {
		t.Fatal("portal execution mutated accepted artifacts")
	}
	if wantFold {
		if &accepted.Entries[0] != &oldFolds.Entries[0] || !enginetest.SamePublishedFold(oldCall, selectedFold(oldFolds.Entries)) {
			t.Fatal("portal execution mutated accepted routine occurrence")
		}
	} else if len(oldFolds.Dependencies) != 0 || freshFolds.Generation != freshAccepted.Generation || len(freshFolds.Entries) != 0 || len(freshFolds.Dependencies) != 0 {
		t.Fatal("portal execution mutated empty accepted frames")
	}
	checkMetadata(oldRoot, oldWant)
	checkMetadata(freshRoot, newWant)
	for _, name := range []string{"rank_headline_old", "rank_headline_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	checkRetained()
	if !old.Retired || !fresh.Retired || oldResource.Root != nil || freshResource.Root != nil || wantFold && accepted.Entries[0].Value.IsNull() {
		t.Fatal("retirement damaged accepted values or retained graph")
	}
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func ftsRankHeadlineRejectStale(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, accepted exec.AcceptedFolds) {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if _, err := scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, d)
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
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch()+1)
	if !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed || local.Analysis != d.Analysis || local.Folds.Current().Generation != accepted.Generation || &local.Folds.Current().Entries[0] != &accepted.Entries[0] || &d.Folds.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("stale rank/headline publication changed accepted graph or backing", err)
	}
}
