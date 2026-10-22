package access

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestOwnTemporaryNamespaceNeedsTheSessionTemporarySchema(t *testing.T) {
	f := newAccessFixture(t)
	toast := *f.temporary
	toast.TemporaryToast = true
	for name, tc := range map[string]struct {
		temp ids.ObjectAddress
		ns   *catalog.Namespace
		want bool
	}{
		"own":                 {f.temporary.Address(), f.temporary, true},
		"other session":       {ids.ObjectAddress{}, f.temporary, false},
		"persistent as temp":  {f.public.Address(), f.public, false},
		"toast":               {f.temporary.Address(), &toast, false},
		"missing namespace":   {f.temporary.Address(), nil, false},
		"different temporary": {f.public.Address(), f.temporary, false},
	} {
		if got := OwnTemporaryNamespace(tc.temp, tc.ns); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestAdjustRelationCreationPersistenceReadsTemporaryLazily(t *testing.T) {
	f := newAccessFixture(t)
	v := f.view(t, "postgres")
	public, temporary := v.Ref(f.public.Address()), v.Ref(f.temporary.Address())
	const otherSession = "cannot create relations in temporary schemas of other sessions"
	for _, tc := range []struct {
		name      string
		ref       ids.ObjectRef
		own       bool
		requested string
		want      string
		message   string
		calls     int
	}{
		{"persistent permanent", public, true, "permanent", "permanent", "", 0},
		{"persistent logged", public, true, "logged", "logged", "", 0},
		{"persistent temporary", public, true, "temporary", "", "cannot create temporary relation in non-temporary schema", 0},
		{"own temporary", temporary, true, "temporary", "temporary", "", 1},
		{"own logged", temporary, true, "logged", "temporary", "", 1},
		{"own permanent", temporary, true, "permanent", "", "only temporary relations may be created in temporary schemas", 1},
		{"other temporary", temporary, false, "temporary", "", otherSession, 1},
		{"other logged", temporary, false, "logged", "", otherSession, 1},
	} {
		calls := 0
		env := countingEnvironment{calls: &calls}
		if tc.own {
			env.temporary = f.temporary.Address()
		}
		got, err := AdjustRelationCreationPersistence(v, env, tc.ref, tc.requested)
		var diagnostic *sqlstate.Error
		switch {
		case tc.message == "" && (err != nil || got != tc.want):
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		case tc.message != "" && (!errors.As(err, &diagnostic) || diagnostic.Code != "42P16" || diagnostic.Message != tc.message || got != ""):
			t.Errorf("%s: got %q, %v; want 42P16 %q", tc.name, got, err, tc.message)
		}
		if calls != tc.calls {
			t.Errorf("%s: environment read %d times, want %d", tc.name, calls, tc.calls)
		}
	}
}
