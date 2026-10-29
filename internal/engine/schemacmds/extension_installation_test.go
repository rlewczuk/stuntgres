package schemacmds_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

// Exercise the ordinary install owner before publication, including providers
// with generated companions. Sealing must revise staged creations without
// treating them as existing base predecessors or inventing recipe slots.
func TestExtensionInstallationStagedAuthority(t *testing.T) {
	for _, name := range []string{"unaccent", "uuid-ossp", "fuzzystrmatch", "pgcrypto", "hstore", "cube", "earthdistance", "citext", "intarray", "intagg", "ltree", "seg", "isn", "lo", "pgrowlocks", "amcheck", "tablefunc"} {
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			if name == "earthdistance" {
				enginetest.RoleExecute(t, e, op, "CREATE EXTENSION cube")
			}
			before := e.CatalogRead()
			e.Mu.Lock()
			stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: name}})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			defer e.DiscardCatalogStage(stage)
			candidate := e.StageRead(stage)
			address := candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name})
			ext := candidate.MustExtension(address)
			if !ext.Installation.Valid() || before.Get(address) != nil || e.CatalogRead().State() != before.State() {
				t.Fatal("script did not remain a sealed unpublished candidate")
			}
			for _, member := range ext.Members {
				d := candidate.Get(member)
				if before.Get(member) != nil || d == nil || catalog.ExtensionMembership(d) != address {
					t.Fatal("invalid created member", member)
				}
				if observed, ok := stage.NameObservation(catalog.DescriptorNameKey(d)); !ok || observed != (ids.ObjectAddress{}) {
					t.Fatal("member revision lost CREATE absence fence", member)
				}
				origin := catalog.DescriptorOrigin(d)
				if slices.Contains(ext.Installation.Members, member) {
					if !origin.Valid(member) || origin.Installation != ext.Installation || ext.Installation.Declarations[origin.Declaration] == d {
						t.Fatal("script declaration lost detached canonical authority", member)
					}
				} else if origin != nil {
					t.Fatal("generated member acquired a fake declaration", member)
				}
			}
			if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
				t.Fatal("complete script candidate rejected", err)
			}
		})
	}
}

func TestExtensionInstallationStockLanguageRetirement(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		commands                    []string
		language, handlers, members int
	}{
		{"extension", []string{"DROP EXTENSION plpgsql CASCADE"}, 0, 0, 0},
		{"detached_validator", []string{"ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)", "DROP EXTENSION plpgsql CASCADE"}, 0, 1, 0},
		{"member_language", []string{"ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()", "DROP FUNCTION pg_catalog.plpgsql_call_handler() CASCADE"}, 0, 0, 0},
		{"detached_language", []string{"ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()", "DROP FUNCTION pg_catalog.plpgsql_call_handler() CASCADE"}, 0, 2, 2},
		{"detached_language_inline", []string{"ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal)", "DROP FUNCTION pg_catalog.plpgsql_inline_handler(internal) CASCADE"}, 0, 2, 2},
		{"detached_language_validator", []string{"ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql", "ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)", "DROP FUNCTION pg_catalog.plpgsql_validator(oid) CASCADE"}, 0, 2, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			baseline := e.CatalogRead()
			enginetest.RoleExecute(t, e, op, "CREATE FUNCTION eo_language_user() RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN 1; END'")
			for _, command := range scenario.commands {
				enginetest.RoleExecute(t, e, op, command)
			}
			view := e.CatalogRead()
			languages, handlers, members := 0, 0, 0
			for _, d := range view.Descriptors() {
				if d.Name() == "eo_language_user" {
					t.Fatal("ordinary language-dependent routine survived CASCADE", d.Address())
				}
				switch d := d.(type) {
				case *catalog.Language:
					if d.Name() == "plpgsql" {
						languages++
					}
				case *catalog.Procedure:
					if d.PLHandler != catalog.NoPLHandler {
						handlers++
						original := baseline.MustProcedure(d.Address())
						if d.Origin != original.Origin || d.Owner != original.Owner || d.Signature != original.Signature || d.Result != original.Result || d.Display != original.Display {
							t.Fatal("retirement rewrote surviving handler authority", d.Address(), d.Name())
						}
					}
				case *catalog.Extension:
					if d.Name() == "plpgsql" {
						if scenario.members == 0 {
							t.Fatal("dependent extension survived retirement", d.Address())
						}
						members = len(d.Members)
					}
				}
				if provider := view.Owner().Provider(d.Address().ClassID); provider != nil {
					if d.Address().ClassID == ids.LanguageClassID || catalog.DescriptorOrigin(d) != nil {
						if err := provider.Validate(view, d); err != nil {
							t.Fatal("surviving authority", d.Address(), d.Name(), err)
						}
					}
				}
			}
			if languages != scenario.language || handlers != scenario.handlers || members != scenario.members {
				t.Fatal("retirement lost genuine language/handler closure", languages, handlers, members)
			}
		})
	}
}

func TestExtensionMemberDependentRetirement(t *testing.T) {
	for _, member := range []struct {
		name, create, add string
	}{
		{"function", "CREATE FUNCTION eo_owned() RETURNS integer RETURN eo_provider()", "ALTER EXTENSION unaccent ADD FUNCTION eo_owned()"},
		{"view", "CREATE VIEW eo_owned AS SELECT eo_provider() AS value", "ALTER EXTENSION unaccent ADD VIEW eo_owned"},
	} {
		t.Run(member.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, command := range []string{
				"CREATE EXTENSION unaccent",
				"CREATE FUNCTION eo_provider() RETURNS integer RETURN 1",
				member.create,
				member.add,
				"CREATE FUNCTION eo_peer() RETURNS integer RETURN eo_provider()",
			} {
				enginetest.RoleExecute(t, e, op, command)
			}
			before := e.CatalogRead()
			ext := before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
			enginetest.RoleExecute(t, e, op, "DROP FUNCTION eo_provider() CASCADE")
			after := e.CatalogRead()
			for address, d := range before.Descriptors() {
				if address == ext || catalog.ExtensionMembership(d) == ext || d.Name() == "eo_provider" || d.Name() == "eo_peer" {
					if after.Get(address) != nil {
						t.Fatal("dependent extension closure survived", address, d.Name())
					}
				} else if after.Get(address) != d {
					t.Fatal("unrelated authority changed", address, d.Name())
				}
			}
		})
	}
}
