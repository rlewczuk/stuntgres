package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// Only the composition's exact isolated membership-repair marker can retain
// historical backing. Similar numeric identities or a broad replay flag cannot.
func TestPGInitPrivsMembershipReplayGuard(t *testing.T) {
	for _, action := range []string{"add", "drop"} {
		for _, mismatch := range []string{"none", "ordinary", "effects", "action", "root", "extension", "domain", "epoch"} {
			t.Run(action+"/"+mismatch, func(t *testing.T) {
				e := engine.New().RestoreStage()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				run := func(sql string) { enginetest.RoleExecute(t, e, op, sql) }
				run("CREATE EXTENSION pageinspect")
				run("CREATE TABLE ip_repair (a integer)")
				run("GRANT SELECT ON ip_repair TO PUBLIC")
				view := e.CatalogRead()
				address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ip_repair"})
				extension := view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pageinspect"})
				var original *catalog.InitialPrivilegeRecord
				if action == "drop" {
					run("ALTER EXTENSION pageinspect ADD TABLE ip_repair")
					original = e.CatalogRead().State().InitialPrivilege(address)
					if original == nil {
						t.Fatal("ordinary ADD did not create repair fixture")
					}
				}
				replay := core.OperationReplay(op)
				before := *replay
				defer func() { *replay = before }()
				replay.StagingEffects = true
				replay.ExtensionMembershipReplayRoot = view.Owner().Domain.Ref(view.Epoch(), address)
				replay.ExtensionMembershipReplayExtension = view.Owner().Domain.Ref(view.Epoch(), extension)
				replay.ExtensionMembershipReplayAction = action
				switch mismatch {
				case "ordinary":
					e.ImportTargets = nil
				case "effects":
					replay.StagingEffects = false
				case "action":
					replay.ExtensionMembershipReplayAction = "schema"
				case "root":
					replay.ExtensionMembershipReplayRoot.Address.ObjectID++
				case "extension":
					replay.ExtensionMembershipReplayExtension.Address.ObjectID++
				case "domain":
					replay.ExtensionMembershipReplayRoot = (&ids.Domain{}).Ref(view.Epoch(), address)
				case "epoch":
					replay.ExtensionMembershipReplayRoot.Epoch++
				}
				run("ALTER EXTENSION pageinspect " + action + " TABLE ip_repair")
				current := e.CatalogRead()
				record := current.State().InitialPrivilege(address)
				if mismatch == "none" {
					if record != original {
						t.Fatal("exact native repair changed historical backing", record, original)
					}
				} else if action == "add" && record == nil || action == "drop" && record != nil {
					t.Fatal("nonmatching marker suppressed ordinary historical bookkeeping", record)
				}
				member := catalog.ExtensionMembership(current.MustRelation(address))
				if action == "add" && member != extension || action == "drop" && member != (ids.ObjectAddress{}) {
					t.Fatal("history exemption bypassed actual membership mutation", member)
				}
			})
		}
	}
}
