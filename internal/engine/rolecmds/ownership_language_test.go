package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/routine"
)

func ownershipLanguageFixture(t *testing.T, installed bool) (*core.Engine, *catalog.Language, authorization.PrincipalRef) {
	t.Helper()
	e, _, _, target, _ := ownershipUnitRoles()
	name := "ownership_language"
	if installed {
		name = "plpgsql"
	} else {
		ownershipUnitExec(t, e, "CREATE TRUSTED LANGUAGE "+name+" HANDLER pg_catalog.plpgsql_call_handler")
	}
	for _, q := range []string{
		"REVOKE USAGE ON LANGUAGE " + name + " FROM PUBLIC",
		"GRANT USAGE ON LANGUAGE " + name + " TO ownership_reader WITH GRANT OPTION",
		"ALTER LANGUAGE " + name + " OWNER TO ownership_source",
		"CREATE FUNCTION pg_catalog.ownership_language_fn(x integer) RETURNS integer LANGUAGE " + name + " AS $$BEGIN RETURN x+1; END$$",
	} {
		ownershipUnitExec(t, e, q)
	}
	d, err := catalog.LookupLanguage(e.CatalogRead(), name)
	if err != nil {
		t.Fatal(err)
	}
	if d.ACL.Explicit != true || len(d.ACL.Entries) != 2 {
		t.Fatal("missing distinguishing owner/reader ACL", d.ACL)
	}
	if (d.Origin != nil) != installed {
		t.Fatal("fixture lacks genuine installed/ordinary distinction")
	}
	return e, d, target
}

func TestRoleOwnershipLanguageRoots(t *testing.T) {
	for _, installed := range []bool{false, true} {
		e, d, _ := ownershipLanguageFixture(t, installed)
		base := e.CatalogRead()
		if !access.OwnershipRoot(base, d, routine.OperatorSeeded) {
			t.Fatal("ownable procedural language omitted", d.Name())
		}
		roots := access.OwnedRoots(base, map[authorization.PrincipalRef]bool{d.Owner: true}, routine.OperatorSeeded)
		if len(roots) != 1 || roots[0] != d.Address() {
			t.Fatal("bulk roots lost language or fabricated consumers", roots)
		}
		for _, oid := range []ids.OID{12, 13, 14} {
			intrinsic := base.Get(ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: oid})
			if intrinsic == nil || access.OwnershipRoot(base, intrinsic, routine.OperatorSeeded) {
				t.Fatal("intrinsic database-system pin became bulk root", oid)
			}
		}
		if !installed {
			ownershipUnitExec(t, e, "ALTER EXTENSION plpgsql ADD LANGUAGE ownership_language")
			member, err := catalog.LookupLanguage(e.CatalogRead(), d.Name())
			if err != nil || member.Origin != nil || member.Extension == (ids.ObjectAddress{}) || !access.OwnershipRoot(e.CatalogRead(), member, routine.OperatorSeeded) {
				t.Fatal("ordinary membership changed root classification or forged origin", err)
			}
		}
	}
}

func TestRoleOwnershipLanguagePublication(t *testing.T) {
	for _, installed := range []bool{false, true} {
		e, old, target := ownershipLanguageFixture(t, installed)
		base, root := e.CatalogRead(), e.Catalog.State
		before := *old
		acl := append([]authorization.ACLEntry(nil), old.ACL.Entries...)
		var consumer *catalog.Procedure
		for d := range catalog.DefinitionProceduresNamedAt(base, nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "ownership_language_fn") {
			consumer = d
		}
		if consumer == nil || consumer.SQL == nil || len(consumer.SQL.Formals) != 1 {
			t.Fatal("missing real retained consumer")
		}
		definition, formal, source := consumer.SQL, &consumer.SQL.Formals[0], consumer.SQL.Source
		stage := e.BeginCatalogStage(base)
		if err := role.StageOwnership(stage, old, target); err != nil {
			t.Fatal(err)
		}
		next, ok := stage.Get(old.Address()).(*catalog.Language)
		if !ok || next == old || next.Address() != old.Address() || next.Revision() <= old.Revision() || next.Owner != target {
			t.Fatal("ownership did not stage same identity/new revision privately", next)
		}
		if e.Catalog.State != root || base.Get(old.Address()) != old || !reflect.DeepEqual(*old, before) {
			t.Fatal("staging changed retained root")
		}
		if !next.ACL.Explicit || len(next.ACL.Entries) != len(acl) || &next.ACL.Entries[0] == &old.ACL.Entries[0] {
			t.Fatal("staged ACL borrowed source backing")
		}
		for i, entry := range acl {
			want := entry
			if want.Grantor == old.Owner {
				want.Grantor = target
			}
			if !want.Grantee.Public && want.Grantee.Role == old.Owner {
				want.Grantee.Role = target
			}
			if next.ACL.Entries[i] != want {
				t.Fatal("owner/grantor/options rewrite", next.ACL.Entries[i], want)
			}
		}
		if next.Call != old.Call || next.Inline != old.Inline || next.Validator != old.Validator || next.Origin != old.Origin || next.Extension != old.Extension || next.Trusted != old.Trusted || next.Procedural != old.Procedural {
			t.Fatal("ownership replaced execution, origin or membership")
		}
		ownershipUnitPublish(t, e, stage)
		current, err := catalog.LookupLanguage(e.CatalogRead(), old.Name())
		if err != nil || current.Owner != target || current.Address() != old.Address() || current.Revision() != next.Revision() {
			t.Fatal("publication lost staged ownership", current, err)
		}
		if base.Get(old.Address()) != old || !reflect.DeepEqual(old.ACL.Entries, acl) || !reflect.DeepEqual(*old, before) {
			t.Fatal("publication mutated retained descriptor/ACL")
		}
		if e.CatalogRead().Get(consumer.Address()) != consumer || consumer.SQL != definition || &definition.Formals[0] != formal || definition.Source != source {
			t.Fatal("ownership rewrote or mutated retained consumer/source backing")
		}
		if current.Origin != old.Origin || current.Extension != old.Extension || (catalog.LanguageProvider{}).Validate(e.CatalogRead(), current) != nil {
			t.Fatal("ownership invalidated genuine language authority")
		}
	}
	for _, explicit := range []bool{false, true} {
		e, old, target := ownershipLanguageFixture(t, false)
		setup := e.BeginCatalogStage(e.CatalogRead())
		withoutEntries := *old
		withoutEntries.ObjectRevision++
		withoutEntries.ACL = authorization.ACLValue{Explicit: explicit}
		if err := setup.Replace(&withoutEntries); err != nil {
			t.Fatal(err)
		}
		ownershipUnitPublish(t, e, setup)
		retained, err := catalog.LookupLanguage(e.CatalogRead(), old.Name())
		if err != nil {
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(e.CatalogRead())
		if err := role.StageOwnership(stage, retained, target); err != nil {
			t.Fatal(err)
		}
		ownershipUnitPublish(t, e, stage)
		current, err := catalog.LookupLanguage(e.CatalogRead(), old.Name())
		if err != nil || current.Owner != target || current.ACL.Explicit != explicit || len(current.ACL.Entries) != 0 || retained.ACL.Explicit != explicit || retained.Owner != old.Owner {
			t.Fatal("ownership collapsed absent/empty ACL or mutated predecessor", err)
		}
	}
}

func TestRoleOwnershipLanguagePublicationFences(t *testing.T) {
	for _, mode := range []string{"cancel", "predecessor"} {
		t.Run(mode, func(t *testing.T) {
			e, old, target := ownershipLanguageFixture(t, false)
			base := e.CatalogRead()
			acl := append([]authorization.ACLEntry(nil), old.ACL.Entries...)
			stage := e.BeginCatalogStage(base)
			if err := role.StageOwnership(stage, old, target); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			publication, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			} else {
				peer := e.BeginCatalogStage(base)
				replacement := *old
				replacement.ObjectRevision++
				replacement.Trusted = false
				if err := peer.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, peer)
			}
			root, roles := e.Catalog.State, e.RoleStore.Published
			_, err = enginetest.PublishCatalog(e, publication, core.PublicationToken{Epoch: base.Epoch()})
			if err == nil {
				t.Fatal("rejected publication installed ownership")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.Catalog.State != root || e.RoleStore.Published != roles || e.CatalogRead().Get(old.Address()).(*catalog.Language).Owner != old.Owner || base.Get(old.Address()) != old || !reflect.DeepEqual(old.ACL.Entries, acl) {
				t.Fatal("failed ownership partially changed current or retained roots")
			}
		})
	}
}
