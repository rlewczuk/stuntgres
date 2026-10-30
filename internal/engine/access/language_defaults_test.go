package access

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestLanguageEffectiveDefaultUsage(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "untrusted"
		if trusted { name = "trusted" }
		t.Run(name, func(t *testing.T) {
			f := newAccessFixture(t)
			actor := f.view(t, "pg_monitor").State.CurrentUser
			inherited := f.view(t, "pg_read_all_stats").State.CurrentUser
			for _, tc := range []struct{name string; explicit, grant, option, owner bool; grantee authorization.PrincipalRef; ordinary, grantable authorization.Privileges}{
				{name: "implicit", ordinary: authorization.UsagePrivilege},
				{name: "explicit-empty", explicit: true},
				{name: "explicit-usage", explicit: true, grant: true, grantee: actor, ordinary: authorization.UsagePrivilege},
				{name: "explicit-option", explicit: true, grant: true, option: true, grantee: actor, ordinary: authorization.UsagePrivilege, grantable: authorization.UsagePrivilege},
				{name: "inherited-option", explicit: true, grant: true, option: true, grantee: inherited, ordinary: authorization.UsagePrivilege, grantable: authorization.UsagePrivilege},
				// Owners may revoke ordinary use from themselves, not their
				// implicit authority to grant it back (explicit empty ACL).
				{name: "owner-empty", explicit: true, owner: true, grantable: authorization.UsagePrivilege},
			} {
				t.Run(tc.name, func(t *testing.T) {
					state := catalog.CloneState(f.read.State())
					language := &catalog.Language{ObjectAddress: ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "acl_language", Owner: f.bootstrap, Procedural: true, Trusted: trusted, ACL: authorization.ACLValue{Explicit: tc.explicit}}
					if tc.owner { language.Owner = actor }
					if tc.grant {
						entry := authorization.ACLEntry{Grantor: f.bootstrap, Grantee: authorization.ACLGrantee{Role: tc.grantee}, Privileges: authorization.UsagePrivilege}
						if tc.option { entry.GrantOptions = authorization.UsagePrivilege }
						language.ACL.Entries = []authorization.ACLEntry{entry}
					}
					state.SetDescriptor(language.Address(), language)
					state.BindName(catalog.DescriptorNameKey(language), language.Address())
					local := f
					local.read = catalog.NewRead(f.owner, state)
					v := local.view(t, "pg_monitor")
					env := countingEnvironment{calls: new(int)}
					for _, options := range []bool{false, true} {
						want := tc.ordinary
						ordinary, grantable := authorization.UsagePrivilege, authorization.Privileges(0)
						if options { want = tc.grantable; ordinary, grantable = 0, authorization.UsagePrivilege }
						got, err := v.Effective(env, v.Ref(language.Address()), options)
						if err != nil || got != want { t.Fatal("language effective rights", trusted, options, got, want, err) }
						inquiry, exists, err := v.ObjectPrivilege(context.Background(), env, actor.ID(), language.Address(), ordinary, grantable)
						if err != nil || !exists || inquiry != (want != 0) { t.Fatal("language inquiry disagrees", inquiry, exists, err) }
					}
					if *env.calls != 0 { t.Fatal("language ACL consulted session TEMP state") }
					if rights, err := local.view(t, "postgres").Effective(env, v.Ref(language.Address()), true); err != nil || rights != authorization.UsagePrivilege { t.Fatal("superuser grant-option authority changed", rights, err) }
					bad := v.Ref(language.Address())
					bad.Epoch++
					if _, err := v.Effective(env, bad, false); err == nil { t.Fatal("stale language reference admitted") }
					bad = new(ids.Domain).Ref(v.Catalog.Epoch(), language.Address())
					if _, err := v.Effective(env, bad, false); err == nil { t.Fatal("foreign language reference admitted") }
				})
			}
		})
	}
}
