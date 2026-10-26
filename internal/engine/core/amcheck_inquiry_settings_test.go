package core_test

import (
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// Use the operational query capture, restricted owner, setting commands and PL
// checkpoint. No retained settings image or large-object frontier is installed.
func TestAmcheckInquiryEffectiveSettings(t *testing.T) {
	for _, caller := range []string{"off", "on"} {
		for _, fail := range []bool{false, true} {
			name := caller + "/success"
			if fail {
				name = caller + "/error"
			}
			t.Run(name, func(t *testing.T) {
				e, index, _ := amcheckUnitRoot(t, false)
				for _, role := range []string{"am_owner", "am_caller", "am_subject"} {
					enginetest.Run(t, e, "CREATE ROLE "+role)
				}
				enginetest.Run(t, e, "ALTER TABLE t OWNER TO am_owner")
				enginetest.Run(t, e, "SELECT lo_create(920291::oid)")
				// The actual restricted actor receives ordinary parameter authority;
				// neither it nor the explicit inquiry subject receives an LO grant.
				enginetest.Run(t, e, "GRANT SET ON PARAMETER lo_compat_privileges TO am_owner")
				parent := e.Catalog.State.MustRelation(index.SQLIndex.Parent)
				owner, _ := e.Roles().Lookup("am_owner")
				subject, _ := e.Roles().Lookup("am_subject")
				if subject.Superuser || subject.Ref == parent.Owner {
					t.Fatal("inquiry subject must be an ordinary nonowner")
				}
				s, _ := blockingUnitOwner(t, e)
				sessionSQL(t, s, "SET lo_compat_privileges="+caller)
				sessionSQL(t, s, "SET ROLE am_caller")
				scope := pgrowlocksUnitScope(t, e, s)
				op := scope.Op
				beforeRole := op.RoleState()
				beforeValues, beforeTyped := maps.Clone(s.Settings.Values), maps.Clone(s.Settings.Typed)
				beforeActions := maps.Clone(s.Settings.Actions)
				e.Mu.Lock()
				source, err := e.CaptureQuerySourcesLocked(scope, nil, true)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				defer source.Close()
				frontier := scope.Inquiry.CommentCatalog
				if frontier.State() == nil {
					t.Fatal("query admission did not capture its catalog")
				}
				invoke := func(query string) (err error) {
					restore := scope.ActivateSQLRoutine(nil, catalog.Read{})
					defer func() { err = restore(err) }()
					finish, err := scope.BeginRoutineCommand()
					if err != nil {
						return err
					}
					defer func() { err = finish(err) }()
					stmt := enginetest.Statement(t, query)
					if err = scope.AcquireRelation(stmt, true); err != nil {
						return err
					}
					result, err := core.EngineExecuteStatement(e, scope, stmt, 0, nil)
					if result.Rows != nil {
						result.Rows.Close()
					}
					return err
				}
				set := func(value string) {
					t.Helper()
					if err := invoke("SET lo_compat_privileges=" + value); err != nil {
						t.Fatal(err)
					}
				}
				host := core.HostOf(scope)
				actor := &functions.AccessRole{Name: "am_subject"}
				check := func(c *exec.Context, value string) {
					t.Helper()
					if got, _ := op.Setting("lo_compat_privileges"); got != value {
						t.Fatalf("effective compatibility = %q, want %q", got, value)
					}
					for _, oid := range []uint32{920291, 920299} {
						for _, privilege := range []string{"SELECT", "UPDATE"} {
							got, exists, err := host.ObjectPrivilege(c, functions.AccessLargeObject, actor, functions.AccessRole{ByOID: true, OID: oid}, privilege)
							present := oid == 920291
							if err != nil || exists != present || got != (present && value == "on") {
								t.Fatalf("%s on %d: value=%v exists=%v error=%v", privilege, oid, got, exists, err)
							}
						}
					}
					if core.ScopeLargeObjectRead(scope) != frontier || op.RoleState() != beforeRole {
						t.Fatal("inquiry changed query frontier or session actor")
					}
					e.Mu.Lock()
					auth, err := scope.RoutineAuthorizationLocked()
					e.Mu.Unlock()
					if err != nil || auth.State.CurrentUser != owner.Ref || scope.Identity.CurrentPrincipal != owner.Ref {
						t.Fatal("inquiry lost restricted current actor", err)
					}
				}
				local := "on"
				if caller == "on" {
					local = "off"
				}
				caught := errors.New("caught inquiry recipe exception")
				err = scope.WithAmcheckOwner(parent, func(c *exec.Context) error {
					check(c, caller)
					set(local)
					check(c, local)
					if err := scope.WithAmcheckOwner(parent, func(nested *exec.Context) error {
						check(nested, local) // Inherit the outer effective image.
						set(caller)
						check(nested, caller)
						return nil
					}); err != nil {
						return err
					}
					check(c, local) // Nested replacement must not leak outward.
					mark, err := scope.BeginPLException()
					if err != nil {
						return err
					}
					set(caller)
					check(c, caller)
					if err := scope.EndPLException(mark, true, caught); err != nil {
						return err
					}
					check(c, local)
					// Genuine post-admission creation distinguishes the active read
					// frontier from current UPDATE authority, even with compat on.
					if _, err := scope.WriteHost().LargeObject(functions.LargeObjectRequest{Name: "lo_create", OID: 920292}); err != nil {
						return err
					}
					for _, tc := range []struct {
						privilege string
						present   bool
					}{{"SELECT", false}, {"UPDATE WITH GRANT OPTION", false}, {"UPDATE", true}, {"SELECT,UPDATE", true}} {
						got, exists, err := host.ObjectPrivilege(c, functions.AccessLargeObject, actor, functions.AccessRole{ByOID: true, OID: 920292}, tc.privilege)
						if err != nil || exists != tc.present || got != (tc.present && local == "on") {
							t.Fatal("active/current frontier selection", tc, got, exists, err)
						}
					}
					check(c, local)
					if fail {
						return caught
					}
					return nil
				})
				if (fail && err != caught) || (!fail && err != nil) {
					t.Fatal("unexpected invocation result", err)
				}
				if value, _ := op.Setting("lo_compat_privileges"); value != caller {
					t.Fatal("caller compatibility not restored", value)
				}
				if op.Restricted != nil || op.RoleState() != beforeRole || scope.Inquiry.CommentCatalog != frontier || core.ScopeLargeObjectRead(scope) != frontier || !reflect.DeepEqual(s.Settings.Values, beforeValues) || !reflect.DeepEqual(s.Settings.Typed, beforeTyped) || !reflect.DeepEqual(s.Settings.Actions, beforeActions) {
					t.Fatal("invocation changed caller settings, actor or frontier")
				}
				address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 920292}
				if frontier.Get(address) != nil {
					t.Fatal("post-admission creation mutated retained query catalog")
				}
			})
		}
	}
}
