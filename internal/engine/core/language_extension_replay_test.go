package core_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestLanguageExtensionReplayProvenance(t *testing.T) {
	source, destination := engine.New(), engine.New()
	seed := source.CatalogRead()
	seedLanguage, err := catalog.LookupLanguage(seed, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	owner := enginetest.StartSession(t, source, nil)
	for _, q := range []string{
		"CREATE ROLE language_member_owner", "CREATE ROLE language_member_reader",
		"CREATE TRUSTED LANGUAGE language_member HANDLER pg_catalog.plpgsql_call_handler INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator",
		"REVOKE USAGE ON LANGUAGE language_member FROM PUBLIC",
		"GRANT USAGE ON LANGUAGE language_member TO language_member_reader WITH GRANT OPTION",
		"ALTER LANGUAGE language_member OWNER TO language_member_owner",
		"ALTER EXTENSION plpgsql ADD LANGUAGE language_member",
		"CREATE FUNCTION pg_catalog.language_member_fn(x integer) RETURNS integer LANGUAGE language_member AS $$BEGIN RETURN x+1; END$$",
		"CREATE OR REPLACE LANGUAGE plpgsql HANDLER pg_catalog.plpgsql_call_handler",
		"CREATE FUNCTION pg_catalog.language_changed_bad() RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN 1;'",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	lookup := func(read catalog.Read, name string) *catalog.Language {
		t.Helper()
		l, err := catalog.LookupLanguage(read, name)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	check := func(read catalog.Read) (*catalog.Language, *catalog.Language) {
		t.Helper()
		alias, installed := lookup(read, "language_member"), lookup(read, "plpgsql")
		if alias.Origin != nil || alias.Extension == (ids.ObjectAddress{}) || alias.Extension != installed.Extension {
			t.Fatal("ordinary member gained installed provenance or lost real membership")
		}
		if !alias.Trusted || !alias.Procedural || alias.Inline == (ids.ObjectAddress{}) || alias.Validator == (ids.ObjectAddress{}) || installed.Trusted || installed.Inline != (ids.ObjectAddress{}) || installed.Validator != (ids.ObjectAddress{}) || alias.Call != installed.Call {
			t.Fatal("mutable language definitions lost genuine handler routes")
		}
		if installed.Origin == nil || !installed.Origin.Valid(installed.Address()) || installed.Origin.Declaration != 3 {
			t.Fatal("changed installed language lost genuine slot authority")
		}
		if !alias.ACL.Explicit || len(alias.ACL.Entries) != 2 {
			t.Fatal("ordinary alias lost explicit distinguishing ACL")
		}
		recipe := installed.Origin.Installation
		ext, ok := read.Get(installed.Extension).(*catalog.Extension)
		if !ok || ext.Installation != recipe || len(recipe.Members) != 4 {
			t.Fatal("installation/member provenance split")
		}
		baseline, ok := recipe.Declarations[3].(*catalog.Language)
		if !ok || !baseline.Trusted || baseline.Inline == (ids.ObjectAddress{}) || baseline.Validator == (ids.ObjectAddress{}) {
			t.Fatal("mutable replacement rewrote sealed released definition")
		}
		for slot, address := range []ids.ObjectAddress{alias.Call, alias.Inline, alias.Validator} {
			handler, ok := read.Get(address).(*catalog.Procedure)
			if !ok || handler.Origin == nil || !handler.Origin.Valid(address) || handler.Origin.Installation != recipe || handler.Origin.Declaration != slot || handler.Extension != ext.Address() || recipe.Members[slot] != address {
				t.Fatal("handler lost genuine reciprocal installation authority", slot)
			}
		}
		for _, l := range []*catalog.Language{alias, installed} {
			if err := (catalog.LanguageProvider{}).Validate(read, l); err != nil {
				t.Fatal("invalid language owner/provider/membership", err)
			}
			found := false
			for _, member := range ext.Members {
				found = found || member == l.Address()
			}
			if !found {
				t.Fatal("missing reciprocal current membership")
			}
		}
		for _, name := range []string{"language_member_fn", "language_changed_bad"} {
			var declaration *catalog.Procedure
			for d := range catalog.DefinitionProceduresNamedAt(read, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, name) {
				declaration = d
			}
			if declaration == nil {
				t.Fatal("missing genuine routine consumer", name)
			}
			if name == "language_member_fn" && (declaration.Language != alias.Address() || declaration.SQL == nil || declaration.SQL.PL == nil) {
				t.Fatal("alias consumer lost checked definition or language identity")
			}
			if name == "language_changed_bad" && (declaration.Language != installed.Address() || declaration.SQL == nil || declaration.SQL.PL != nil || declaration.SQL.PLSyntax != nil) {
				t.Fatal("installed no-validator source was eagerly compiled")
			}
		}
		return alias, installed
	}
	original := source.CatalogRead()
	alias, installed := check(original)
	if installed.Address() != seedLanguage.Address() || installed.Origin != seedLanguage.Origin || installed.Extension != seedLanguage.Extension || installed.Revision() <= seedLanguage.Revision() || seed.Get(seedLanguage.Address()) != seedLanguage || !seedLanguage.Trusted || seedLanguage.Inline == (ids.ObjectAddress{}) || seedLanguage.Validator == (ids.ObjectAddress{}) {
		t.Fatal("replacement changed sealed identity or mutated retained seed backing")
	}
	aliasValue, installedValue := *alias, *installed
	aliasValue.ACL.Entries = slices.Clone(alias.ACL.Entries)
	var saved bytes.Buffer
	if err := engine.Dump(context.Background(), source, &saved); err != nil {
		t.Fatal(err)
	}
	destinationOwner := enginetest.StartSession(t, destination, nil)
	for _, q := range []string{"CREATE ROLE language_member_owner", "CREATE ROLE language_member_reader", "CREATE TRUSTED LANGUAGE language_filler HANDLER pg_catalog.plpgsql_call_handler"} {
		enginetest.SessionSQL(t, destinationOwner, q)
	}
	for _, sameEngine := range []bool{false, true} {
		target := destination
		if sameEngine {
			target = source
		}
		var previousAlias, previousInstalled *catalog.Language
		var previousAliasValue, previousInstalledValue catalog.Language
		for range 2 {
			if err := engine.Restore(context.Background(), target, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			a, l := check(target.CatalogRead())
			if a == alias || l == installed || a.Owner == alias.Owner && target == destination || a.Origin != nil || l.Origin == installed.Origin || l.Origin.Installation == installed.Origin.Installation {
				t.Fatal("replay borrowed source declaration/principal/installation backing")
			}
			if len(a.ACL.Entries) == 0 || &a.ACL.Entries[0] == &alias.ACL.Entries[0] {
				t.Fatal("replay borrowed source ACL backing")
			}
			if previousAlias != nil && (a == previousAlias || l == previousInstalled || !reflect.DeepEqual(*previousAlias, previousAliasValue) || !reflect.DeepEqual(*previousInstalled, previousInstalledValue)) {
				t.Fatal("repeated replay reused or mutated retained descriptor backing")
			}
			previousAlias, previousInstalled = a, l
			previousAliasValue, previousInstalledValue = *a, *l
			previousAliasValue.ACL.Entries = slices.Clone(a.ACL.Entries)
		}
	}
	if original.Get(alias.Address()) != alias || original.Get(installed.Address()) != installed || !reflect.DeepEqual(*alias, aliasValue) || !reflect.DeepEqual(*installed, installedValue) {
		t.Fatal("replay mutated retained source definitions/ACL/origin backing")
	}
}
