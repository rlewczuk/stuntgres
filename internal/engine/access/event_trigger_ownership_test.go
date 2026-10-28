package access

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestEventTriggerOwnershipEligibility(t *testing.T) {
	f := newAccessFixture(t)
	d := &catalog.EventTrigger{ObjectAddress: ids.ObjectAddress{ClassID: ids.EventTriggerClassID, ObjectID: 30001}, ObjectRevision: 1, ObjectName: "et_owned", Owner: f.bootstrap, FiringMode: catalog.TriggerOrigin}
	state := catalog.CloneState(f.read.State())
	state.SetDescriptor(d.Address(), d)
	state.BindName(catalog.DescriptorNameKey(d), d.Address())
	f.read = f.read.WithState(state)
	env := countingEnvironment{calls: new(int)}
	for _, tc := range []struct{ actor, target, message, hint string }{
		{"postgres", "postgres", "", ""},
		{"postgres", "pg_monitor", `permission denied to change owner of event trigger "et_owned"`, "The owner of an event trigger must be a superuser."},
		{"pg_monitor", "pg_monitor", "must be owner of event trigger et_owned", ""},
		{"pg_monitor", "postgres", "must be owner of event trigger et_owned", ""},
	} {
		v := f.view(t, tc.actor)
		target := v.Roles.LookupRef(tc.target)
		err := EventTriggerOwnershipEligibility(v, env, d, target)
		if tc.message == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) {
			t.Fatal(tc, err)
		}
		got := *diagnostic
		got.File, got.Line, got.Routine = "", "", ""
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: tc.message, Hint: tc.hint}
		if got != want {
			t.Fatal("complete ownership diagnostic", got, want)
		}
	}
}
