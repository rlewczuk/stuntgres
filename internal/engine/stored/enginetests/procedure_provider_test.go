package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// The procedure class validator owns builtin, SQL and conversion routines and
// hands installed extension members to the extension validator, which accepts
// nothing else.
func TestExtensionProviderValidatesOnlyInstalledProcedures(t *testing.T) {
	e := engine.New()
	for _, name := range []string{"cube", "unaccent", "hstore", "intarray", "fuzzystrmatch", "intagg", "pgcrypto", `"uuid-ossp"`} {
		enginetest.Run(t, e, "CREATE EXTENSION "+name)
	}
	enginetest.Run(t, e, "CREATE FUNCTION procedure_provider_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	view := e.CatalogRead()
	if _, ok := view.Owner().Provider(ids.ProcedureClassID).(stored.ProcedureProvider); !ok {
		t.Fatalf("registered procedure provider: %T", view.Owner().Provider(ids.ProcedureClassID))
	}
	members := extension.CatalogProvider{ids.ProcedureClassID}
	counts := map[string]int{}
	var member, builtin *catalog.Procedure
	for _, d := range view.Descriptors() {
		proc, ok := d.(*catalog.Procedure)
		if !ok {
			continue
		}
		if err := (stored.ProcedureProvider{}).Validate(view, proc); err != nil {
			t.Fatalf("procedure %s: %v", proc.Name(), err)
		}
		memberErr := members.Validate(view, proc)
		switch {
		case proc.Implementation != nil:
			if memberErr != nil {
				t.Fatalf("member %s: %v", proc.Name(), memberErr)
			}
			counts["member"]++
			if extension.HstoreProviderOf(proc) == extension.StockHstore {
				counts["hstore"]++
			}
			member = proc
		case memberErr == nil:
			t.Fatalf("extension validator accepted non-member %s", proc.Name())
		case proc.SQL != nil:
			counts["sql"]++
		case proc.Builtin != nil:
			counts["builtin"]++
			builtin = proc
		case proc.Conversion.Name() != "":
			counts["conversion"]++
		}
	}
	for _, kind := range []string{"member", "sql", "builtin", "conversion"} {
		if counts[kind] == 0 {
			t.Fatalf("no %s procedure validated: %v", kind, counts)
		}
	}
	if counts["hstore"] != 4 {
		t.Fatalf("validated hstore procedures %d want 4", counts["hstore"])
	}
	stripped := *member
	stripped.Implementation = nil
	if (stored.ProcedureProvider{}).Validate(view, &stripped) == nil {
		t.Fatal("member without its implementation accepted")
	}
	claimed := *builtin
	claimed.Implementation = extension.StockPgcrypto
	if (stored.ProcedureProvider{}).Validate(view, &claimed) == nil {
		t.Fatal("builtin with an extension implementation accepted")
	}
}
