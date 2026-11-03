package textsearch_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFTSArrayConfigPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT array_to_tsvector(ARRAY['b','a','a']) AS v")
	if d.Folds.Current().Generation != 0 || len(d.Folds.Current().Entries) != 0 {
		t.Fatal("Describe executed constructor")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, sqltype.TSVector, "'a' 'b'")
	before := enginetest.SnapshotFoldDescription(d)
	analysis := d.Analysis
	for _, setting := range []string{"simple", "english", "simple"} {
		enginetest.SessionSQL(t, s, "SET default_text_search_config='"+setting+"'")
		enginetest.PreparedFoldExecute(t, e, s, ast, &d)
		current := d.Folds.Current()
		if current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, sqltype.TSVector, "'a' 'b'")) {
			t.Fatal("constructor accepted value/generation/backing changed")
		}
	}
	// The shared cancellation witness mutates only the actual private candidate,
	// then proves the accepted generation and original backing remain untouched.
	enginetest.ConvertRejectPublication(t, e, s, ast, d, before.Accepted)
	// A genuine stale-epoch acceptance failure must also publish no fragment.
	func() {
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
		publication.Group.Entries = append([]exec.FoldConstant{}, publication.Group.Entries...)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch()+1)
		if !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed || local.Analysis != d.Analysis || d.Analysis != analysis || local.Folds.Current().Generation != before.Accepted.Generation || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("failed private publication changed accepted graph/artifact", err)
		}
	}()

	parameterAST, parameter := enginetest.PreparedDescription(t, e, s, "SELECT array_to_tsvector($1::text[]) AS v")
	for _, tc := range []struct{ input, want string }{{"{b,a,a}", "'a' 'b'"}, {"{c,b}", "'b' 'c'"}} {
		enginetest.ConvertOwnedExecute(t, e, s, parameterAST, &parameter, tc.input, tc.want)
		for _, entry := range parameter.Folds.Current().Entries {
			if entry.Result.Type.ID() == sqltype.TSVector {
				t.Fatal("parameter result became immutable fold", entry)
			}
		}
	}
	func() {
		op, err := s.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &parameter
		result, err := scope.Execute(parameterAST, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, in sqltype.InputEnvironment) error {
			value, err := info.Types[0].Type.InputWithEnvironment(ctx, "{a,NULL}", -1, in)
			frame[0] = value
			return err
		})
		finish := scope.Finish(err)
		var diagnostic *sqlstate.Error
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("failed constructor published a cursor")
		}
		if !errors.As(finish, &diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "lexeme array may not contain nulls" {
			t.Fatal("parameter execution failure", err, finish)
		}
		if current := d.Folds.Current(); current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, sqltype.TSVector, "'a' 'b'")) {
			t.Fatal("failed sibling execution changed retained literal")
		}
	}()
	enginetest.ConvertOwnedExecute(t, e, s, parameterAST, &parameter, "{z,z}", "'z'")

	currentAST, current := enginetest.PreparedDescription(t, e, s, "SELECT get_current_ts_config() AS cfg")
	if current.Folds.Current().Generation != 0 || len(current.Folds.Current().Entries) != 0 {
		t.Fatal("Describe evaluated stable current configuration")
	}
	english := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "english"})
	if english.ObjectID == 0 {
		t.Fatal("missing real English configuration descriptor")
	}
	for _, tc := range []struct {
		name string
		oid  uint32
	}{{"simple", 3748}, {"english", uint32(english.ObjectID)}, {"simple", 3748}} {
		enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.name+"'")
		r, err := enginetest.PreparedExecute(e, s, currentAST, &current)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]sqltype.Value, 1)
		present, err := r.Rows.Next(t.Context(), values)
		if err != nil || !present || values[0].Type().ID() != sqltype.Regconfig {
			r.Rows.Close()
			t.Fatal("live typed current configuration", present, err)
		}
		oid, err := values[0].RegTSOID()
		if err != nil || oid != tc.oid {
			r.Rows.Close()
			t.Fatal("stale prepared configuration", oid, err)
		}
		more, err := r.Rows.Next(t.Context(), values)
		r.Rows.Close()
		if more || err != nil {
			t.Fatal("current configuration row count", more, err)
		}
		for _, fold := range current.Folds.Current().Entries {
			if fold.Result.Type.ID() == sqltype.Regconfig {
				t.Fatal("stable identity captured in immutable artifacts", fold)
			}
		}
		if current.Regtypes != nil && len(current.Regtypes.Constants) != 0 {
			t.Fatal("implicit configuration captured", current.Regtypes)
		}
	}
}
