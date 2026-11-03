package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"testing"
)

func resetStatsACLReplay(t *testing.T) {
	for _, state := range []string{"bootstrap", "null", "empty", "grant_option"} {
		t.Run(state, func(t *testing.T) {
			source := engine.New()
			actor := enginetest.StartSession(t, source, nil)
			statsFunctionExec(t, actor, "CREATE ROLE reset_acl_reader")
			owner, reader := source.Roles().LookupRef("postgres"), source.Roles().LookupRef("reset_acl_reader")
			for oid := range routine.RestrictedStatsResetRoutines() {
				address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
				p, _ := source.CatalogRead().Procedure(address)
				acl := p.ACL
				switch state {
				case "null":
					acl = authorization.ACLValue{}
				case "empty":
					acl = authorization.ACLValue{Explicit: true}
				case "grant_option":
					acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}, {Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}}}
				}
				if err := enginetest.SetObjectACL(source, context.Background(), source.Catalog.Domain.Ref(source.CatalogRead().Epoch(), address), acl); err != nil {
					t.Fatal(err)
				}
			}
			var stream bytes.Buffer
			if err := engine.Dump(context.Background(), source, &stream); err != nil {
				t.Fatal(err)
			}
			if state == "null" && !strings.Contains(stream.String(), "proacl=NULL") {
				t.Fatal("raw NULL repair omitted")
			}
			target := engine.New()
			targetActor := enginetest.StartSession(t, target, nil)
			statsFunctionExec(t, targetActor, "CREATE ROLE reset_acl_reader")
			if err := engine.Restore(context.Background(), target, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			for oid := range routine.RestrictedStatsResetRoutines() {
				address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
				from, _ := source.CatalogRead().Procedure(address)
				to, _ := target.CatalogRead().Procedure(address)
				beforeTarget := target.CatalogRead().State()
				canceled, cancel := context.WithCancel(context.Background())
				cancel()
				if err := engine.Restore(canceled, target, bytes.NewReader(stream.Bytes())); !errors.Is(err, context.Canceled) || target.CatalogRead().State() != beforeTarget {
					t.Fatal("canceled replay changed existing ACL authority", err)
				}
				if from.ACL.Explicit != to.ACL.Explicit || len(from.ACL.Entries) != len(to.ACL.Entries) {
					t.Fatal("raw authority lost", state, from.ACL, to.ACL)
				}
				for i, entry := range from.ACL.Entries {
					got := to.ACL.Entries[i]
					grantor, _ := source.Roles().Descriptor(entry.Grantor)
					if got.Grantor != target.Roles().LookupRef(grantor.Name) || got.Privileges != entry.Privileges || got.GrantOptions != entry.GrantOptions || got.Grantee.Public != entry.Grantee.Public {
						t.Fatal("grantor/mask replay", got, entry)
					}
					if !entry.Grantee.Public {
						grantee, _ := source.Roles().Descriptor(entry.Grantee.Role)
						if got.Grantee.Role != target.Roles().LookupRef(grantee.Name) {
							t.Fatal("principal remap", got, entry)
						}
					}
				}
				if to.Builtin == nil || to.Builtin.Identity.ID != oid || to.Builtin.Kind != catalog.FunctionRoutine {
					t.Fatal("replay replaced declaration identity")
				}
			}
			statsFunctionExec(t, targetActor, "SET ROLE reset_acl_reader")
			for _, call := range []string{"SELECT pg_stat_reset_replication_slot(NULL::text)", "SELECT pg_stat_reset_subscription_stats(NULL::oid)", "SELECT pg_stat_reset_shared(NULL::text)", "SELECT pg_stat_reset_slru(NULL::text)", "SELECT pg_stat_reset_backend_stats(0)"} {
				err := resetStatsSQL(t, targetActor, call)
				allowed := state == "null" || state == "grant_option"
				if (err == nil) != allowed {
					t.Fatal("raw ACL invocation semantics", state, call, err)
				}
			}
			statsFunctionExec(t, targetActor, "RESET ROLE")
			if err := engine.Reset(context.Background(), target); err != nil {
				t.Fatal(err)
			}
			for oid := range routine.RestrictedStatsResetRoutines() {
				p, _ := target.CatalogRead().Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
				bootstrap := target.Roles().LookupRef("postgres")
				if !p.ACL.Explicit || len(p.ACL.Entries) != 1 || p.ACL.Entries[0] != (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: bootstrap}, Privileges: authorization.ExecutePrivilege}) {
					t.Fatal("Reset lost bootstrap ACL", p.ACL)
				}
			}
		})
	}
}
