package textsearch_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

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
			ast, d := enginetest.PreparedDescription(t, e, s, query)
			if len(d.Folds.Current().Entries) != 0 || d.Folds.Current().Generation != 0 {
				t.Fatal("Describe executed immutable query")
			}
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			entry := enginetest.PreparedFoldEntry(t, d, sqltype.TSQuery, "")
			before := enginetest.SnapshotFoldDescription(d)
			analysis := d.Analysis
			for _, a := range []ids.ObjectAddress{address, configuration.Parser, dictionary} {
				dep := enginetest.DictionaryFoldDependency(t, d, a)
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
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			after := d.Folds.Current()
			if d.Analysis == analysis || enginetest.DictionaryFoldDependency(t, d, dictionary).Revision != e.CatalogRead().Get(dictionary).Revision() {
				t.Fatal("dictionary edit bypassed reanalysis")
			}
			if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, sqltype.TSQuery, "")) {
				t.Fatal("accepted query backing replaced")
			}
			freshAST, fresh := enginetest.PreparedDescription(t, e, s, query)
			enginetest.PreparedFoldExecute(t, e, s, freshAST, &fresh)
			enginetest.PreparedFoldEntry(t, fresh, sqltype.TSQuery, "'the'")
			runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT "+name+"('public.convert_fold'::regconfig,$1::text) AS normalized")
			enginetest.ConvertOwnedExecute(t, e, s, runtimeAST, &runtime, "The", "'the'")
			enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH CONFIGURATION public.convert_fold DROP MAPPING FOR asciiword")
			enginetest.PreparedFoldExecute(t, e, s, ast, &d)
			if current := d.Folds.Current(); current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] {
				t.Fatal("mapping revision invalidated immutable value")
			}
			enginetest.ConvertOwnedExecute(t, e, s, runtimeAST, &runtime, "The", "")
			if len(runtime.Folds.Current().Entries) != 0 {
				t.Fatal("parameter conversion folded")
			}
			enginetest.ConvertRejectPublication(t, e, s, ast, d, before.Accepted)
		})
	}
	t.Run("default_live_not_captured", func(t *testing.T) {
		for _, name := range []string{"to_tsvector", "to_tsquery", "plainto_tsquery", "phraseto_tsquery", "websearch_to_tsquery"} {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
			ast, d := enginetest.PreparedDescription(t, e, s, "SELECT "+name+"('Rats'::text) AS normalized")
			simple, english := "'rats'", "'rat'"
			if name == "to_tsvector" {
				simple, english = "'rats':1", "'rat':1"
			}
			for _, tc := range []struct{ setting, want string }{{"simple", simple}, {"english", english}, {"simple", simple}} {
				enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.setting+"'")
				enginetest.ConvertOwnedExecute(t, e, s, ast, &d, "", tc.want)
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
