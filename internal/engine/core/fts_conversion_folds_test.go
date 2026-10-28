package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func convertOwnedExecute(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d *core.Description, input, want string) {
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

func TestFTSConvertPreparedOwnership(t *testing.T) {
	for _, name := range []string{"to_tsquery", "plainto_tsquery", "phraseto_tsquery", "websearch_to_tsquery"} {
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.convert_fold_dict (TEMPLATE=simple,STOPWORDS=english)")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH CONFIGURATION public.convert_fold (COPY=pg_catalog.simple)")
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.convert_fold ALTER MAPPING FOR asciiword WITH public.convert_fold_dict")
			address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "convert_fold"})
			dictionary := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "convert_fold_dict"})
			configuration := e.CatalogRead().MustTSConfiguration(address)
			query := "SELECT " + name + "('public.convert_fold'::regconfig,'The'::text) AS normalized"
			ast, d := sequencePreparedDescription(t, e, s, query)
			if len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
				t.Fatal("Describe executed immutable query")
			}
			preparedFoldExecute(t, e, s, ast, &d)
			entry := preparedFoldEntry(t, d, sqltype.TSQuery, "")
			before := snapshotFoldDescription(d)
			analysis := d.Analysis
			for _, a := range []ids.ObjectAddress{address, configuration.Parser, dictionary} {
				dep := dictionaryFoldDependency(t, d, a)
				if dep.Revision != e.CatalogRead().Get(a).Revision() {
					t.Fatal("missing actual operand revision", dep)
				}
				for _, foldDep := range before.Accepted.Dependencies {
					if foldDep.Ref.Address == a {
						t.Fatal("operand became executable fold authority", foldDep)
					}
				}
			}
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.convert_fold_dict (STOPWORDS)")
			preparedFoldExecute(t, e, s, ast, &d)
			after := d.Folds.Current()
			if d.Analysis == analysis || dictionaryFoldDependency(t, d, dictionary).Revision != e.CatalogRead().Get(dictionary).Revision() {
				t.Fatal("dictionary edit bypassed reanalysis")
			}
			if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, sqltype.TSQuery, "")) {
				t.Fatal("accepted query backing replaced")
			}
			freshAST, fresh := sequencePreparedDescription(t, e, s, query)
			preparedFoldExecute(t, e, s, freshAST, &fresh)
			preparedFoldEntry(t, fresh, sqltype.TSQuery, "'the'")
			runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT "+name+"('public.convert_fold'::regconfig,$1::text) AS normalized")
			convertOwnedExecute(t, e, s, runtimeAST, &runtime, "The", "'the'")
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.convert_fold DROP MAPPING FOR asciiword")
			preparedFoldExecute(t, e, s, ast, &d)
			if current := d.Folds.Current(); current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("mapping revision invalidated immutable value")
			}
			convertOwnedExecute(t, e, s, runtimeAST, &runtime, "The", "")
			if len(runtime.Folds.Current().Entries) != 0 {
				t.Fatal("parameter conversion folded")
			}
			convertRejectPublication(t, e, s, ast, d, before.Accepted)
		})
	}
	t.Run("default_live_not_captured", func(t *testing.T) {
		for _, name := range []string{"to_tsvector", "to_tsquery", "plainto_tsquery", "phraseto_tsquery", "websearch_to_tsquery"} {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
			ast, d := sequencePreparedDescription(t, e, s, "SELECT "+name+"('Rats'::text) AS normalized")
			simple, english := "'rats'", "'rat'"
			if name == "to_tsvector" {
				simple, english = "'rats':1", "'rat':1"
			}
			for _, tc := range []struct{ setting, want string }{{"simple", simple}, {"english", english}, {"simple", simple}} {
				enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.setting+"'")
				convertOwnedExecute(t, e, s, ast, &d, "", tc.want)
				for _, entry := range d.Folds.Current().Entries {
					if entry.Result.Type.ID() == sqltype.TSVector || entry.Result.Type.ID() == sqltype.TSQuery {
						t.Fatal("default stable conversion acquired accepted immutable result", entry)
					}
				}
				if d.Regtypes != nil && len(d.Regtypes.Constants) != 0 {
					t.Fatal("omitted default captured regconfig", d.Regtypes)
				}
			}
		}
	})
}

func convertRejectPublication(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, d core.Description, accepted exec.AcceptedFolds) {
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
	local := cloneFoldDescription(t, d)
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
