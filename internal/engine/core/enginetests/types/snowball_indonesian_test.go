package types_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestSnowballIndonesianPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.indonesian_fold (TEMPLATE=snowball,Language=indonesian,StopWords=english)")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.indonesian_fold")
	const q = "SELECT ts_lexize('public.indonesian_fold'::regdictionary,'THE'::text) AS lexemes"
	ast, d := enginetest.PreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("description evaluated dictionary")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 1009, "{}")
	before := enginetest.SnapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "indonesian_fold"})
	if _, ok := e.CatalogRead().LookupType(1009); !ok {
		t.Fatal("TEXT[] missing")
	}
	old := e.CatalogRead().MustTSDictionary(a)
	operand := enginetest.DictionaryFoldDependency(t, d, a)
	analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.indonesian_fold (stopwords)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current.Runtime == old.Runtime || current.Revision() == operand.Revision {
		t.Fatal("replacement runtime authority not published")
	}
	oldValue, err := old.Runtime.Lexize(t.Context(), "THE")
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 0 {
		t.Fatal("retained old runtime", oldValue, err)
	}
	newValue, err := current.Runtime.Lexize(t.Context(), "THE")
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 1 || newValue.Lexemes[0].Text != "the" {
		t.Fatal(newValue, err)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	refreshed := enginetest.DictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, 1009, "{}")) {
		t.Fatal("accepted immutable provider fold lost backing/current analysis")
	}
	freshAST, fresh := enginetest.PreparedDescription(t, e, s, q)
	enginetest.PreparedFoldExecute(t, e, s, freshAST, &fresh)
	enginetest.PreparedFoldEntry(t, fresh, 1009, "{the}")
	runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT ts_lexize('public.indonesian_fold'::regdictionary,$1::text) AS lexemes")
	for _, tc := range []struct{ input, want string }{{"THE", "{the}"}, {"MENYAPU", "{sapu}"}, {"RATS", "{rats}"}} {
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
			scope.Expected = &runtime
			r, err := scope.Execute(runtimeAST, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
				value, err := info.Types[0].Type.InputWithEnvironment(ctx, tc.input, -1, input)
				frame[0] = value
				return err
			})
			if err = scope.Finish(err); err != nil {
				t.Fatal(err)
			}
			got := enginetest.Tuples(r)
			r.Rows.Close()
			if len(got) != 1 || got[0][0] != tc.want {
				t.Fatal("current parameter provider", tc.input, got)
			}
		}()
	}
	for _, dep := range before.Accepted.Dependencies {
		if dep.Ref.Address == a {
			t.Fatal("operand retained as executable authority")
		}
	}
	enginetest.PreparedFoldEntry(t, before, 1009, "{}")
	failedAST, failed := enginetest.PreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := enginetest.PreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("later failure published Snowball prefix", err)
	}
	enginetest.SnowballRejectedFold(t, e, s, ast, d, "{}")
}
