package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
)

func TestBtreeGINCompoundHelperOwnerACL(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			e, _, _ := compoundEngine(t, populated)
			for _, q := range []string{
				"DROP INDEX compound_private_i",
				"CREATE ROLE compound_private_owner", "CREATE ROLE compound_private_reader",
				"GRANT USAGE,CREATE ON SCHEMA public TO compound_private_owner",
				"REVOKE EXECUTE ON FUNCTION public.gin_enum_cmp(anyenum,anyenum) FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION public.gin_enum_cmp(anyenum,anyenum) TO compound_private_reader WITH GRANT OPTION",
				"GRANT EXECUTE ON FUNCTION public.gin_enum_cmp(anyenum,anyenum) TO compound_private_owner",
				"CREATE INDEX compound_private_i ON compound_private USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)",
			} {
				enginetest.Run(t, e, q)
			}
			idx, original := ginInstalledRoot(t, e, "compound_private_i")
			view := e.CatalogRead()
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(original.Gin.Columns[2].Binding.Class.Support(1).Routine)}
			old := view.MustProcedure(address)
			oldACL := old.ACL
			oldACL.Entries = slices.Clone(oldACL.Entries)
			before, entries := ginCopyPayload(original.Gin), gistCopyEntries(original)
			owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("compound_private_owner")
			reader := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("compound_private_reader")
			if owner.ID() == 0 || reader.ID() == 0 || !old.ACL.Explicit || len(old.ACL.Entries) != 3 {
				t.Fatal("fixture lacks actual explicit grants, including the destination-owner coalescing case")
			}
			oldACLBacking := &old.ACL.Entries[0]
			assertProjection := func(current catalog.Read, currentIndex *catalog.Relation, root *storage.IndexRoot) {
				t.Helper()
				compoundSharedPresentation(t, original, root)
				helper := current.MustProcedure(address)
				if helper.Owner != owner || helper.Origin != old.Origin || helper.Implementation != old.Implementation || !helper.ACL.Explicit || len(helper.ACL.Entries) != 2 {
					t.Fatal("lawful owner projection lost genuine helper authority")
				}
				seenOwner, seenReader := false, false
				for _, entry := range helper.ACL.Entries {
					if entry.Grantor != owner || entry.Grantee.Public || entry.Privileges != authorization.ExecutePrivilege {
						t.Fatal("unexpected actual owner-projected grant", entry)
					}
					switch entry.Grantee.Role {
					case owner:
						if seenOwner || entry.GrantOptions != 0 {
							t.Fatal("owner grant projection", entry)
						}
						seenOwner = true
					case reader:
						if seenReader || entry.GrantOptions != authorization.ExecutePrivilege {
							t.Fatal("reader grant option projection", entry)
						}
						seenReader = true
					default:
						t.Fatal("owner projection retained an old/foreign grantee", entry)
					}
				}
				if !seenOwner || !seenReader || helper.Revision() == old.Revision() {
					t.Fatal("ownership did not advance genuine helper revision")
				}
				captured := root.Gin.Columns[2].Binding.Class.Support(1)
				if captured.RoutineRevision != uint64(helper.Revision()) || captured.Authority != original.Gin.Columns[2].Binding.Class.Support(1).Authority || currentIndex.SQLIndex.Keys[2].Class != root.Gin.Columns[2].Binding.Class {
					t.Fatal("owner projection lost actual refreshed root/key captures")
				}
				if _, err := routine.BindClassSupport(current, idx.SQLIndex.Keys[2].Class); err == nil {
					t.Fatal("owner projection laundered detached predecessor capture")
				}
			}
			assertOriginal := func() {
				t.Helper()
				if e.CatalogRead().State() != view.State() || e.Storage.IndexRoots[idx.Address()] != original || &old.ACL.Entries[0] != oldACLBacking || !reflect.DeepEqual(old.ACL, oldACL) || !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(original.Entries, entries) {
					t.Fatal("rejected/private owner change mutated committed predecessor")
				}
			}
			const alter = "ALTER FUNCTION public.gin_enum_cmp(anyenum,anyenum) OWNER TO compound_private_owner"
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := enginetest.Execute(e, canceled, enginetest.Statement(t, alter), nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled owner transfer", err)
			}
			assertOriginal()
			session := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, session)
			enginetest.SessionSQL(t, session, "INSERT INTO compound_private VALUES(8,'111','10','m','0.02','{2}','private:1')")
			writes := session.Transactions.Writes[idx.SQLIndex.Parent]
			enginetest.SessionSQL(t, session, alter)
			e.Mu.Lock()
			privateView := e.PrivateCatalogLocked(session.Transactions.Catalog)
			privateIndex := privateView.MustRelation(idx.Address())
			privateRoot := index.EffectiveRoot(e.Storage.IndexRoots, session.Transactions.Catalog, idx.Address())
			e.Mu.Unlock()
			assertProjection(privateView, privateIndex, privateRoot)
			nextWrites := session.Transactions.Writes[idx.SQLIndex.Parent]
			if len(writes) != len(nextWrites) || &writes[0].Values[0] != &nextWrites[0].Values[0] {
				t.Fatal("owner projection reconstructed private values")
			}
			if output, ok := writes[0].IndexOutputs[idx.Address()]; ok {
				next := nextWrites[0].IndexOutputs[idx.Address()]
				if next.Index != privateIndex || !slices.Equal(next.Keys, output.Keys) || len(output.Keys) > 0 && &next.Keys[0] != &output.Keys[0] {
					t.Fatal("owner projection reconstructed private produced keys")
				}
			}
			assertOriginal()
			enginetest.TransactionEnd(t, session, tx, false)
			assertOriginal()
			// Valid principals alone cannot authorize an ACL difference unrelated to
			// exact ownership projection. Each candidate remains unpublished.
			for _, kind := range []string{"public", "grantor", "grantee", "grant-options", "unchanged-owner", "canceled"} {
				t.Run(kind, func(t *testing.T) {
					stage := e.BeginCatalogStage(view)
					forged := *old
					forged.Owner = owner
					forged.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
						{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
						{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
					}}
					ctx := context.Background()
					switch kind {
					case "public":
						forged.ACL.Entries = append(forged.ACL.Entries, authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege})
					case "grantor":
						forged.ACL.Entries[1].Grantor = old.Owner
					case "grantee":
						forged.ACL.Entries[1].Grantee.Role = old.Owner
					case "grant-options":
						forged.ACL.Entries[1].GrantOptions = 0
					case "unchanged-owner":
						forged.Owner = old.Owner
						forged.ACL = oldACL
						forged.ACL.Entries = slices.Clone(oldACL.Entries)
						forged.ACL.Entries = append(forged.ACL.Entries, authorization.ACLEntry{Grantor: old.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege})
					case "canceled":
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					e.Mu.Lock()
					forged.ObjectRevision, err = e.Catalog.RevisionLocked()
					e.Mu.Unlock()
					if err != nil {
						e.DiscardCatalogStage(stage)
						t.Fatal(err)
					}
					if err := stage.Replace(&forged); err != nil {
						e.DiscardCatalogStage(stage)
						t.Fatal(err)
					}
					prepared, err := e.ValidateCatalogStage(ctx, stage)
					e.DiscardCatalogStage(stage)
					if err == nil || prepared != nil || kind == "canceled" && !errors.Is(err, context.Canceled) {
						t.Fatal("owner certificate admitted unrelated ACL or lost cancellation", kind, prepared, err)
					}
					assertOriginal()
				})
			}
			enginetest.Run(t, e, alter)
			currentIndex, currentRoot := ginInstalledRoot(t, e, "compound_private_i")
			assertProjection(e.CatalogRead(), currentIndex, currentRoot)
			enginetest.Run(t, e, "INSERT INTO compound_private VALUES(8,'111','10','m','0.02','{2}','after:1')")
			_, written := ginInstalledRoot(t, e, "compound_private_i")
			if len(written.Entries) != len(original.Entries)+1 {
				t.Fatal("owner projection lost maintained scalar entries")
			}
			if &old.ACL.Entries[0] != oldACLBacking || !reflect.DeepEqual(old.ACL, oldACL) || !reflect.DeepEqual(original.Gin, before) || !reflect.DeepEqual(original.Entries, entries) {
				t.Fatal("owner projection or later write mutated retained original ACL/root")
			}
		})
	}
	// NULL and explicitly empty ACLs are different genuine catalog states.
	// Their owner transfers preserve that distinction; a fabricated flip does not.
	for _, explicit := range []bool{false, true} {
		name := "null-acl"
		if explicit {
			name = "empty-acl"
		}
		t.Run(name, func(t *testing.T) {
			e, _, _ := compoundEngine(t, true)
			enginetest.Run(t, e, "CREATE ROLE compound_empty_owner")
			enginetest.Run(t, e, "GRANT USAGE,CREATE ON SCHEMA public TO compound_empty_owner")
			if explicit {
				for _, q := range []string{"DROP INDEX compound_private_i", "REVOKE ALL ON FUNCTION public.gin_enum_cmp(anyenum,anyenum) FROM PUBLIC", "REVOKE ALL ON FUNCTION public.gin_enum_cmp(anyenum,anyenum) FROM postgres", "CREATE INDEX compound_private_i ON compound_private USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)"} {
					enginetest.Run(t, e, q)
				}
			}
			idx, root := ginInstalledRoot(t, e, "compound_private_i")
			view := e.CatalogRead()
			address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(root.Gin.Columns[2].Binding.Class.Support(1).Routine)}
			old := view.MustProcedure(address)
			if old.ACL.Explicit != explicit || len(old.ACL.Entries) != 0 {
				t.Fatal("fixture did not retain genuine NULL/explicit-empty ACL", old.ACL)
			}
			owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("compound_empty_owner")
			source, entries := ginCopyPayload(root.Gin), gistCopyEntries(root)
			stage := e.BeginCatalogStage(view)
			forged := *old
			forged.Owner = owner
			forged.ACL.Explicit = !explicit
			e.Mu.Lock()
			var err error
			forged.ObjectRevision, err = e.Catalog.RevisionLocked()
			e.Mu.Unlock()
			if err != nil {
				e.DiscardCatalogStage(stage)
				t.Fatal(err)
			}
			if err := stage.Replace(&forged); err != nil {
				e.DiscardCatalogStage(stage)
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			e.DiscardCatalogStage(stage)
			if err == nil || prepared != nil || e.CatalogRead().State() != view.State() || e.Storage.IndexRoots[idx.Address()] != root {
				t.Fatal("certificate confused NULL and explicitly empty ACL", prepared, err)
			}
			enginetest.Run(t, e, "ALTER FUNCTION public.gin_enum_cmp(anyenum,anyenum) OWNER TO compound_empty_owner")
			_, next := ginInstalledRoot(t, e, "compound_private_i")
			compoundSharedPresentation(t, root, next)
			helper := e.CatalogRead().MustProcedure(address)
			if helper.Owner != owner || helper.ACL.Explicit != explicit || len(helper.ACL.Entries) != 0 || old.ACL.Explicit != explicit || !reflect.DeepEqual(root.Gin, source) || !reflect.DeepEqual(root.Entries, entries) {
				t.Fatal("ordinary owner projection changed NULL/empty distinction or retained root")
			}
		})
	}
}
