package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The procedure class validator owns builtin, SQL and conversion routines and
// hands installed extension members to the extension validator, which accepts
// nothing else.
func TestExtensionProviderValidatesOnlyInstalledProcedures(t *testing.T) {
	e := New()
	for _, name := range []string{"cube", "unaccent", "intarray", "fuzzystrmatch", "intagg", "pgcrypto", `"uuid-ossp"`} {
		run(t, e, "CREATE EXTENSION "+name)
	}
	run(t, e, "CREATE FUNCTION procedure_provider_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	view := e.CatalogRead()
	if _, ok := view.Owner().Provider(ids.ProcedureClassID).(procedureProvider); !ok {
		t.Fatalf("registered procedure provider: %T", view.Owner().Provider(ids.ProcedureClassID))
	}
	members := extensionCatalogProvider{ids.ProcedureClassID}
	counts := map[string]int{}
	var member, builtin *catalog.Procedure
	for _, d := range view.Descriptors() {
		proc, ok := d.(*catalog.Procedure)
		if !ok {
			continue
		}
		if err := (procedureProvider{}).Validate(view, proc); err != nil {
			t.Fatalf("procedure %s: %v", proc.Name(), err)
		}
		memberErr := members.Validate(view, proc)
		switch {
		case proc.Implementation != nil:
			if memberErr != nil {
				t.Fatalf("member %s: %v", proc.Name(), memberErr)
			}
			counts["member"]++
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
	stripped := *member
	stripped.Implementation = nil
	if (procedureProvider{}).Validate(view, &stripped) == nil {
		t.Fatal("member without its implementation accepted")
	}
	claimed := *builtin
	claimed.Implementation = stockPgcrypto
	if (procedureProvider{}).Validate(view, &claimed) == nil {
		t.Fatal("builtin with an extension implementation accepted")
	}
}
