package enginetest

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DictionaryFoldDependency returns the analysis dependency on address and requires it to be an
// operand dependency.
func DictionaryFoldDependency(t testing.TB, d core.Description, address ids.ObjectAddress) catalog.ExpressionDependency {
	t.Helper()
	for _, dependency := range d.Analysis.Dependencies {
		if dependency.Ref.Address == address {
			if dependency.Purpose != catalog.OperandDependency {
				t.Fatal("dictionary operand acquired executable purpose", dependency)
			}
			return dependency
		}
	}
	t.Fatal("dictionary identity missing from analysis manifest")
	return catalog.ExpressionDependency{}
}

// DictionaryFoldRuntimeExecute executes ast against d with the text parameter "YeS".
func DictionaryFoldRuntimeExecute(e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) (core.Result, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return core.Result{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	scope.Expected = d
	r, err := scope.Execute(ast, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		value, err := info.Types[0].Type.InputWithEnvironment(ctx, "YeS", -1, input)
		frame[0] = value
		return err
	})
	return r, scope.Finish(err)
}

// ConvertOwnedExecute executes ast against d with input as its parameter and requires the one
// row want.
func ConvertOwnedExecute(t testing.TB, e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description, input, want string) {
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
	scope.Expected = d
	count := len(d.Parameters.Types)
	r, err := scope.Execute(ast, count, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
		if len(info.Types) == 0 {
			return nil
		}
		v, err := info.Types[0].Type.InputWithEnvironment(ctx, input, -1, environment)
		frame[0] = v
		return err
	})
	finish := scope.Finish(err)
	if err != nil || finish != nil {
		t.Fatal(err, finish)
	}
	defer r.Rows.Close()
	values := make([]sqltype.Value, 1)
	present, err := r.Rows.Next(t.Context(), values)
	if err != nil || !present || values[0].IsNull() {
		t.Fatal(present, values, err)
	}
	text, err := values[0].Type().Output(t.Context(), values[0])
	if err != nil || text != want {
		t.Fatal(text, want, err)
	}
	if more, err := r.Rows.Next(t.Context(), values); err != nil || more {
		t.Fatal(more, err)
	}
}

// ConvertRejectPublication cancels a private fold publication of ast and requires the accepted
// generation and backing to stay unchanged.
func ConvertRejectPublication(t testing.TB, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, accepted exec.AcceptedFolds) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
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
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || local.Folds.Current().Generation != accepted.Generation || &local.Folds.Current().Entries[0] != &accepted.Entries[0] || &d.Folds.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected private query publication mutated accepted backing", err)
	}
}

// PreparedFoldExecute executes ast against d and closes its rows.
func PreparedFoldExecute(t testing.TB, e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description) {
	t.Helper()
	r, err := PreparedExecute(e, s, ast, d)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		r.Rows.Close()
	}
}

// PreparedFoldProtocolCommand runs fn in a protocol command scope of s on cycle.
func PreparedFoldProtocolCommand(t testing.TB, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle, fn func(*core.StatementScope) error) {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	err = fn(scope)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
}

// SavedFoldDescription is a description with its fold owner state captured at one point.
//
// Save actual immutable accepted payloads, never a shallow copy of a mutable owner.
type SavedFoldDescription struct {
	core.Description
	Accepted              exec.AcceptedFolds
	Environment           core.PreparedEnvironment
	Live, ReusableBacking bool
}

// SnapshotFoldDescription captures the accepted folds of d.
func SnapshotFoldDescription(d core.Description) SavedFoldDescription {
	return SavedFoldDescription{Description: d, Accepted: d.Folds.Current(), Environment: d.Folds.Environment, Live: d.Folds.Live(), ReusableBacking: FoldBackingReusable(d.Folds)}
}

// FoldBackingReusable reports whether the next planning attempt borrows the accepted entries.
//
// Stored origins can remain operationally reusable after root invalidation.
// Observe the candidate actually lent to planning, including stored-only groups;
// private eligibility itself is asserted in the owner's state-machine tests.
func FoldBackingReusable(owner *core.PreparedFolds) bool {
	if owner == nil || !owner.Live() {
		return false
	}
	current := owner.Current()
	next := owner.Begin()
	return len(current.Entries) != 0 && len(next.Candidate.Entries) == len(current.Entries) && &next.Candidate.Entries[0] == &current.Entries[0]
}

// CloneFoldDescription returns d with a new fold owner that accepted the same entries.
func CloneFoldDescription(t testing.TB, d core.Description) core.Description {
	t.Helper()
	owner := core.NewPreparedFolds()
	owner.Analysis, owner.Environment, owner.RoleSensitive = d.Folds.Analysis, d.Folds.Environment, d.Folds.RoleSensitive
	attempt := owner.Begin()
	attempt.Candidate.Entries = d.Folds.Current().Entries
	attempt.Candidate.Dependencies = d.Folds.Current().Dependencies
	attempt.Candidate.Private, attempt.Candidate.DependenciesPrivate = true, true
	if _, err := owner.Accept(&attempt); err != nil {
		t.Fatal(err)
	}
	d.Folds = owner
	return d
}

// PreparedFoldEntry returns the accepted fold constant of type id whose output is text.
func PreparedFoldEntry(t testing.TB, description any, id sqltype.ID, text string) exec.FoldConstant {
	t.Helper()
	var entries []exec.FoldConstant
	switch d := description.(type) {
	case core.Description:
		if !FoldBackingReusable(d.Folds) {
			t.Fatal("no accepted live fold group")
		}
		entries = d.Folds.Current().Entries
	case SavedFoldDescription:
		if !d.Live || !d.ReusableBacking {
			t.Fatal("no saved accepted live fold group")
		}
		entries = d.Accepted.Entries
	default:
		t.Fatalf("unexpected description %T", description)
	}
	for _, entry := range entries {
		if entry.Result.Type.ID() != id {
			continue
		}
		if entry.Value.IsNull() && text == "NULL" {
			return entry
		}
		got, err := entry.Result.Type.Output(context.Background(), entry.Value)
		if err == nil && got == text {
			return entry
		}
	}
	t.Fatalf("missing owned %d datum %q in %+v", id, text, entries)
	return exec.FoldConstant{}
}

// SamePublishedFold reports whether two fold constants publish the same planning payload.
//
// Compare the public planning payload, not private input representation. Real
// second-use backing/hit checks accompany these comparisons; exec's owner-local
// tests distinguish provider markers from omitted and invalid literal inputs.
func SamePublishedFold(a, b exec.FoldConstant) bool {
	callable := a.Callable == b.Callable
	if !callable {
		left, lok := a.Callable.(*catalog.Procedure)
		right, rok := b.Callable.(*catalog.Procedure)
		callable = lok && rok && exec.SameRoutineDeclaration(left, right)
	}
	if a.Offset != b.Offset || a.Occurrence != b.Occurrence || a.Source != b.Source || a.Origin != b.Origin || a.Frame != b.Frame || a.Provider != b.Provider || !callable || a.Operator != b.Operator || a.Result != b.Result || !sqltype.SameDatum(a.Value, b.Value) || !a.Coercion.Equal(b.Coercion) || len(a.Inputs) != len(b.Inputs) {
		return false
	}
	for i, input := range a.Inputs {
		other := b.Inputs[i]
		if input.Usage != other.Usage || input.UnknownNull != other.UnknownNull {
			return false
		}
		if input.Value.Type() == nil && other.Value.Type() == nil {
			if input.Value != other.Value {
				return false
			}
		} else if !sqltype.SameDatum(input.Value, other.Value) {
			return false
		}
	}
	return true
}

// SnowballRejectedFold cancels a fold publication of ast and requires the accepted Snowball
// fold want to stay in place.
func SnowballRejectedFold(t testing.TB, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, want string) {
	t.Helper()
	before := SnapshotFoldDescription(d)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
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
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("canceled Snowball fold accepted", err)
	}
	if local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("rejection changed accepted generation/backing")
	}
	PreparedFoldEntry(t, before, 1009, want)
}
