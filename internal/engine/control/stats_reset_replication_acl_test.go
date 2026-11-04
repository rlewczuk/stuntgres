package control

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"testing"
)

func TestStatsResetReplicationACLReconstruction(t *testing.T) {
	owner := authorization.PrincipalRef{}
	procedures := routine.BuiltinProcedures(owner)
	routine.SeedStatsResetACLs(procedures, owner)
	var seen []ids.OID
	for _, p := range procedures {
		if !routine.RestrictedStatsResetRoutine(p.Address().ObjectID) {
			if recoveryControlACLReconstructed(&p, owner) {
				t.Fatal("unrelated declaration omitted", p.Name())
			}
			continue
		}
		seen = append(seen, p.Address().ObjectID)
		if !recoveryControlACLReconstructed(&p, owner) {
			t.Fatal("exact bootstrap seed not reconstructed", p.Name())
		}
		for _, mutation := range []string{"null", "empty", "public", "duplicate", "mask", "grant_option", "name", "signature", "namespace", "class", "subid", "provider", "oid"} {
			changed := p
			changed.ACL.Entries = slices.Clone(p.ACL.Entries)
			switch mutation {
			case "null":
				changed.ACL = authorization.ACLValue{}
			case "empty":
				changed.ACL.Entries = nil
			case "public":
				changed.ACL.Entries[0].Grantee = authorization.ACLGrantee{Public: true}
			case "duplicate":
				changed.ACL.Entries = append(changed.ACL.Entries, changed.ACL.Entries[0])
			case "mask":
				changed.ACL.Entries[0].Privileges = 0
			case "grant_option":
				changed.ACL.Entries[0].GrantOptions = authorization.ExecutePrivilege
			case "name":
				changed.ObjectName = "changed"
			case "signature":
				changed.Signature = "23"
				if p.Signature == "23" {
					changed.Signature = "25"
				}
			case "namespace":
				changed.ObjectNamespace.ObjectID = ids.PublicNamespaceOID
			case "class":
				changed.ObjectAddress.ClassID = ids.RelationClassID
			case "subid":
				changed.ObjectAddress.SubID = 1
			case "provider":
				changed.Builtin = nil
			case "oid":
				changed.ObjectAddress.ObjectID = 6006
			}
			if recoveryControlACLReconstructed(&changed, owner) {
				t.Fatal("modified raw authority omitted", p.Name(), mutation)
			}
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []ids.OID{2274, 2307, 3775, 3776, 3777, 6170, 6232, 6387}) {
		t.Fatal("restricted roster", seen)
	}
	for _, identity := range []string{"pg_catalog.pg_stat_reset()", "pg_catalog.pg_stat_reset_single_table_counters(oid)", "pg_catalog.pg_stat_reset_single_function_counters(oid)", "pg_catalog.pg_stat_reset_slru(text)", "pg_catalog.pg_stat_reset_shared(text)", "pg_catalog.pg_stat_reset_backend_stats(integer)", "pg_catalog.pg_stat_reset_replication_slot(text)", "pg_catalog.pg_stat_reset_subscription_stats(oid)"} {
		for _, value := range []struct {
			sql, text string
			absent    bool
		}{{"NULL", "", true}, {emptyObjectACLExpression, "{}", false}, {"E'{postgres=X/postgres,reset_reader=X*/postgres}'", "{postgres=X/postgres,reset_reader=X*/postgres}", false}} {
			query := "UPDATE pg_catalog.pg_proc SET proacl=" + value.sql + "::pg_catalog.aclitem[] WHERE oid=E'" + identity + "'::pg_catalog.regprocedure"
			statements, err := parser.Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, err)
			}
			want := ObjectACLRepair{Class: ids.ProcedureClassID, Identity: identity, Text: value.text, Absent: value.absent}
			if got, ok := ParseObjectACLRepair(statements[0].(*parser.Update)); !ok || got != want {
				t.Fatal("canonical raw repair", got, want, ok)
			}
		}
	}
}
