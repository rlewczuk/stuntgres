package core_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

func TestStatisticsObjectOwnerAuthority(t *testing.T) {
	for _, mode := range []string{"exact", "no_import", "missing_pair", "foreign_ref", "stale_epoch", "wrong_class", "subobject", "wrong_recipient", "identity_target", "unqualified", "wrong_namespace", "wrong_name", "wrong_kind", "if_exists", "replaced_definition", "recreated_definition", "replaced_namespace", "recreated_namespace", "temporary_namespace", "retired_recipient", "recreated_recipient", "retired_creator", "recreated_creator"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := statisticsUnitEngine(t)
			aclUnitRoles(e, "statistics_recipient", "statistics_other")
			recipient := e.Roles().LookupRef("statistics_recipient")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			stage, err := statisticsUnitStage(t, e, op, statisticsUnitCreate())
			if err != nil {
				t.Fatal(err)
			}
			d := statisticsUnitDescriptor(t, stage)
			extensionUnitPublish(t, e, op, stage)
			e.DiscardCatalogStage(stage)
			e.ImportTargets = map[authorization.PrincipalRef]authorization.PrincipalRef{recipient: recipient}
			ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
			pair, err := schemacmds.CaptureRestoredStatisticsOwner(e, op, ref, recipient)
			if err != nil {
				t.Fatal(err)
			}
			raw := &parser.AlterOwner{Kind: "statistics", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: pair.Namespace.Name()}, {Text: d.Name()}}}, Target: parser.RoleSpec{Name: "statistics_recipient"}}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			switch mode {
			case "no_import":
				e.ImportTargets = nil
			case "missing_pair":
				pair = ddl.RestoredStatisticsOwner{}
			case "foreign_ref":
				pair.Ref = (&ids.Domain{}).Ref(ref.Epoch, ref.Address)
			case "stale_epoch":
				pair.Ref.Epoch++
			case "wrong_class":
				pair.Ref.Address.ClassID = ids.RelationClassID
			case "subobject":
				pair.Ref.Address.SubID = 1
			case "wrong_recipient":
				raw.Target.Name = "statistics_other"
			case "identity_target":
				raw.Target.Identity = "current_user"
				raw.Target.Name = ""
			case "unqualified":
				raw.Name.Parts = raw.Name.Parts[1:]
			case "wrong_namespace":
				raw.Name.Parts[0].Text = "other"
			case "wrong_name":
				raw.Name.Parts[1].Text = "other"
			case "wrong_kind":
				raw.Kind = "table"
			case "if_exists":
				raw.IfExists = true
			case "replaced_definition", "recreated_definition":
				n := *d
				n.ObjectRevision = d.Revision() + 1
				if mode == "recreated_definition" {
					n.ObjectAddress.ObjectID += 100000
					state.RemoveDescriptor(d.Address())
					state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				}
				state.SetDescriptor(n.Address(), &n)
			case "replaced_namespace", "recreated_namespace", "temporary_namespace":
				ns := *pair.Namespace
				ns.ObjectRevision++
				if mode == "recreated_namespace" {
					ns.ObjectAddress.ObjectID += 100000
					state.RemoveDescriptor(pair.Namespace.Address())
					state.BindName(catalog.DescriptorNameKey(&ns), ns.Address())
				}
				if mode == "temporary_namespace" {
					ns.Temporary = op.Session.SessionToken
				}
				state.SetDescriptor(ns.Address(), &ns)
			case "retired_recipient", "recreated_recipient", "retired_creator", "recreated_creator":
				roles := cloneRoleFixture(e.RoleStore.Published)
				old := recipient
				if mode == "retired_creator" || mode == "recreated_creator" {
					old = pair.Creator
				}
				role := fixtureRole(roles, old)
				retireFixtureRole(roles, old)
				if mode == "recreated_recipient" || mode == "recreated_creator" {
					role.Ref = e.RoleStore.Domain.Principal(old.ID() + 100000)
					putFixtureRole(roles, role)
				}
				e.RoleStore.PublishLocked(roles)
			}
			if mode == "replaced_definition" || mode == "recreated_definition" || mode == "replaced_namespace" || mode == "recreated_namespace" || mode == "temporary_namespace" {
				e.Catalog.PublishLocked(state)
			}
			e.Mu.Unlock()
			before := e.CatalogRead()
			err = schemacmds.RestoreStatisticsOwner(e, op, raw, pair)
			if mode != "exact" {
				if err == nil || e.CatalogRead().State() != before.State() {
					t.Fatal("invalid pair published", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after := e.CatalogRead()
			changed, ok := after.Statistics(d.Address())
			if !ok || changed == d || changed.Owner != recipient || changed.Revision() <= d.Revision() || changed.Selection != d.Selection || changed.Relation != d.Relation || changed.Namespace() != d.Namespace() {
				t.Fatal("owner COW changed definition facts", changed)
			}
			if err = schemacmds.RestoreStatisticsOwner(e, op, raw, pair); err == nil || e.CatalogRead().State() != after.State() {
				t.Fatal("consumed descriptor pair reused", err)
			}
			if d.Owner != pair.Creator || before.Get(d.Address()) != d {
				t.Fatal("old capture mutated")
			}
		})
	}
}
