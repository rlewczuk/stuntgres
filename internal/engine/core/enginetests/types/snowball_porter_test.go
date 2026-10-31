package types_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestSnowballPorterPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.snowball_fold (TEMPLATE=snowball,Language=porter,StopWords=english)")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.snowball_fold")
	q := "SELECT ts_lexize('public.snowball_fold'::regdictionary,'ONLY'::text) AS lexemes"
	ast, d := enginetest.PreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("description evaluated Porter")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 1009, "{}")
	before := enginetest.SnapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "snowball_fold"})
	old := e.CatalogRead().MustTSDictionary(a)
	operand := enginetest.DictionaryFoldDependency(t, d, a)
	analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.snowball_fold (stopwords)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current == old || current.Runtime == old.Runtime || current.Revision() == operand.Revision {
		t.Fatal("replacement authority not published")
	}
	oldValue, err := old.Runtime.Lexize(t.Context(), "ONLY")
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 0 {
		t.Fatal(oldValue, err)
	}
	newValue, err := current.Runtime.Lexize(t.Context(), "ONLY")
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 1 || newValue.Lexemes[0].Text != "onli" || newValue.Lexemes[0].Prefix || newValue.Lexemes[0].Variant != 0 {
		t.Fatal(newValue, err)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	refreshed := enginetest.DictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, 1009, "{}")) {
		t.Fatal("accepted immutable Porter backing/provenance lost or analysis not refreshed")
	}
	freshAST, fresh := enginetest.PreparedDescription(t, e, s, q)
	enginetest.PreparedFoldExecute(t, e, s, freshAST, &fresh)
	enginetest.PreparedFoldEntry(t, fresh, 1009, "{onli}")
	runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT ts_lexize('public.snowball_fold'::regdictionary,$1::text) AS lexemes")
	// The genuine helper supplies YeS: historical Porter must yield ye, not yes.
	r, err := enginetest.DictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
	if err != nil {
		t.Fatal(err)
	}
	got := enginetest.Tuples(r)
	r.Rows.Close()
	if len(got) != 1 || got[0][0] != "{ye}" {
		t.Fatal(got)
	}
	for _, dep := range before.Accepted.Dependencies {
		if dep.Ref.Address == a {
			t.Fatal("fold retained operand as executable authority")
		}
	}
	enginetest.PreparedFoldEntry(t, before, 1009, "{}")
	failedAST, failed := enginetest.PreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := enginetest.PreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("later failure published Porter prefix", err)
	}
	enginetest.SnowballRejectedFold(t, e, s, ast, d, "{}")
}
