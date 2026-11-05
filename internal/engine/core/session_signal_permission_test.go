package core

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
)

func TestSessionSignalPermissionRetiredPrincipal(t *testing.T) {
	domain, foreign := new(authorization.Domain), new(authorization.Domain)
	root := authorization.Seed(domain)
	admin := domain.Principal(authorization.BootstrapPrincipalID)
	original := authorization.RoleDescriptor{Ref: domain.Principal(20000), Name: "retired_owner", Login: true, Superuser: true, Inherit: true}
	var err error
	root, err = root.PutDescriptor(original)
	if err != nil {
		t.Fatal(err)
	}
	retainedRoot := root
	before := retainedRoot.Read(authorization.PrincipalRef{})
	root = root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{original.Ref: original})
	replacement := original
	replacement.Ref, replacement.Superuser = domain.Principal(20001), false
	root, err = root.PutDescriptor(replacement)
	if err != nil {
		t.Fatal(err)
	}
	roles := root.Read(authorization.PrincipalRef{})
	signal, ok := roles.Lookup("pg_signal_backend")
	if !ok {
		t.Fatal("missing predefined signal authority")
	}
	if _, ok := roles.Find(original.Ref); ok {
		t.Fatal("retired descriptor survived")
	}
	if retained, err := before.Descriptor(original.Ref); err != nil || retained != original {
		t.Fatal("retirement mutated retained role root", retained, err)
	}
	for _, kind := range []functions.SessionSignalKind{functions.SignalCancel, functions.SignalTerminate} {
		for _, tc := range []struct {
			name          string
			roles         authorization.Read
			actor, victim authorization.PrincipalRef
			want          string
		}{
			{"superuser_retired_victim", roles, admin, original.Ref, ""},
			{"signal_retired_victim", roles, signal.Ref, original.Ref, ""},
			{"replacement_no_adoption", roles, replacement.Ref, original.Ref, "denied"},
			{"extant_superuser_protected", before, signal.Ref, original.Ref, "superuser"},
			{"superuser_zero_victim", roles, admin, authorization.PrincipalRef{}, "stuntgres: foreign or zero principal"},
			{"signal_foreign_victim", roles, signal.Ref, foreign.Principal(original.Ref.ID()), "stuntgres: foreign or zero principal"},
			{"superuser_foreign_victim", roles, admin, foreign.Principal(original.Ref.ID()), "stuntgres: foreign or zero principal"},
			{"retired_actor_no_self_shortcut", roles, original.Ref, original.Ref, "stuntgres: stale principal"},
			{"foreign_actor", roles, foreign.Principal(admin.ID()), original.Ref, "stuntgres: foreign or zero principal"},
			{"zero_actor", roles, authorization.PrincipalRef{}, original.Ref, "stuntgres: foreign or zero principal"},
		} {
			t.Run(kind.Name()+"/"+tc.name, func(t *testing.T) {
				err := sessionSignalPermission(tc.roles, tc.actor, tc.victim, kind)
				if tc.want == "" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if tc.want == "denied" || tc.want == "superuser" {
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to cancel query", Detail: `Only roles with privileges of the role whose query is being canceled or with privileges of the "pg_signal_backend" role may cancel this query.`}
					if tc.want == "superuser" {
						want.Detail = "Only roles with the SUPERUSER attribute may cancel queries of roles with the SUPERUSER attribute."
					}
					if kind == functions.SignalTerminate {
						want.Message = "permission denied to terminate process"
						want.Detail = `Only roles with privileges of the role whose process is being terminated or with privileges of the "pg_signal_backend" role may terminate this process.`
						if tc.want == "superuser" {
							want.Detail = "Only roles with the SUPERUSER attribute may terminate processes of roles with the SUPERUSER attribute."
						}
					}
					var got *sqlstate.Error
					if !errors.As(err, &got) || *got != want {
						t.Fatal("permission diagnostic", err, want)
					}
					return
				}
				if err == nil || err.Error() != tc.want {
					t.Fatal(err, tc.want)
				}
			})
		}
	}
}
