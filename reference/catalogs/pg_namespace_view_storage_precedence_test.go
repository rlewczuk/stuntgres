package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGNamespaceViewStoragePrecedence(t *testing.T) {
	pgNamespaceScenario(t, nil, []string{"ns18_view_acl_creator"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, owner, "CREATE ROLE ns18_view_acl_creator", "SET ROLE ns18_view_acl_creator", "CREATE TEMP TABLE ns18_view_acl_source(v INT)")
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO ns18_view_acl_source VALUES(99)", 1))
		pgNamespaceExec(t, p, b, owner,
			"CREATE TEMP VIEW ns18_view_acl_direct AS SELECT v FROM ns18_view_acl_source",
			"CREATE TEMP VIEW ns18_view_acl_nested AS SELECT v FROM ns18_view_acl_direct",
			"CREATE TEMP VIEW ns18_view_acl_outer_invoker WITH (security_invoker=true) AS SELECT v FROM ns18_view_acl_direct",
			"CREATE TEMP VIEW ns18_view_acl_invoker WITH (security_invoker=true) AS SELECT v FROM ns18_view_acl_source")
		actor := pgNamespaceRoleID(t, p, owner, "ns18_view_acl_creator")
		name, id, toast, toastID := pgNamespaceTempPair(t, p, b, owner, "ns18_view_acl_source", actor)
		views := []string{"ns18_view_acl_direct", "ns18_view_acl_nested", "ns18_view_acl_outer_invoker"}
		for _, view := range append(append([]string(nil), views...), "ns18_view_acl_invoker") {
			pgNamespaceExec(t, p, b, owner, "GRANT SELECT ON "+view+" TO PUBLIC")
		}
		// Revocation is actual table ACL state, including the owner's SELECT;
		// namespace visibility and the peer's outer-view access remain valid.
		pgNamespaceExec(t, p, b, owner, "REVOKE SELECT ON ns18_view_acl_source FROM ns18_view_acl_creator")
		for _, view := range views {
			for _, prepared := range []bool{false, true} {
				denied := fixture.ImplicitError("SELECT v FROM "+name+"."+view, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ns18_view_acl_source"})
				denied.SelectRows, denied.Prepared, denied.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, denied)
			}
		}
		physical := func(view string) {
			for _, prepared := range []bool{false, true} {
				query := "SELECT v FROM " + name + "." + view
				if b.PG {
					// Keep the independent small foreign-buffer witness, not a
					// fictitious copy of native physical-storage exclusion.
					step := fixture.Select(query, []string{"v"}, []string{"INT4"}, nil)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, step)
				} else {
					denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "cannot access temporary tables of other sessions"})
					denied.SelectRows, denied.Prepared, denied.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, denied)
				}
			}
		}
		// Original bootstrap invoker has source access even while the definer
		// lacks it. Nested invoker does not replace its inner definer's owner.
		physical("ns18_view_acl_invoker")
		pgNamespaceExec(t, p, b, owner, "GRANT SELECT ON ns18_view_acl_source TO ns18_view_acl_creator")
		for _, view := range views {
			physical(view)
		}
		ownRows := fixture.Select("SELECT v FROM ns18_view_acl_source", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, ownRows)
		fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Prepared(ownRows))
		for _, view := range views {
			rows := fixture.Select("SELECT v FROM "+view, []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, rows)
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Prepared(rows))
		}
		pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
		pgNamespaceRow(t, p, b, peer, toast, toastID, 10, nil)
		pgNamespaceExec(t, p, b, owner, "RESET ROLE")
	})
}
