package routine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAggregateOrderedCreatorState(t *testing.T) {
	var internal, integer sqltype.Usage
	for _, typ := range sqltype.Builtins() {
		switch typ.ID() {
		case 2281:
			internal = typ.Usage()
		case sqltype.Int4:
			integer = typ.Usage()
		}
	}
	if internal.Type == nil || integer.Type == nil {
		t.Fatal("missing actual state type descriptors")
	}
	for _, tc := range []struct {
		name string
		state sqltype.Usage
		superuser, denied bool
	}{
		{"internal ordinary creator", internal, false, true},
		{"internal superuser creator", internal, true, false},
		{"concrete ordinary creator", integer, false, false},
		{"concrete superuser creator", integer, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAggregateCreatorState(tc.state, tc.superuser)
			if !tc.denied {
				if err != nil { t.Fatal(err) }
				return
			}
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "aggregate transition data type cannot be internal"}
			var actual *sqlstate.Error
			if !errors.As(err, &actual) || !reflect.DeepEqual(actual, want) {
				t.Fatalf("creator diagnostic = %#v (%v), want %#v including absent position", actual, err, want)
			}
		})
	}
}
