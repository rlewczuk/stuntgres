package session

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAssignModesValidation(t *testing.T) {
	serializable := parser.TransactionMode{Isolation: parser.IsolationSerializable}
	readOnly := parser.TransactionMode{AccessSet: true, ReadOnly: true}
	readWrite := parser.TransactionMode{AccessSet: true}
	deferrable := parser.TransactionMode{DeferrableSet: true, Deferrable: true}
	queried := func() *TransactionOwner { return &TransactionOwner{SnapshotSet: true} }
	nested := func() *TransactionOwner { return &TransactionOwner{Savepoints: make([]SavepointMark, 1)} }
	for _, tc := range []struct {
		name    string
		owner   func() *TransactionOwner
		modes   []parser.TransactionMode
		message string
	}{
		{"isolation after query", queried, []parser.TransactionMode{serializable}, "SET TRANSACTION ISOLATION LEVEL must be called before any query"},
		{"isolation in subtransaction", nested, []parser.TransactionMode{serializable}, "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction"},
		{"read-write in read-only subtransaction", func() *TransactionOwner { t := nested(); t.ReadOnly = true; return t }, []parser.TransactionMode{readWrite}, "cannot set transaction read-write mode inside a read-only transaction"},
		{"read-write after query", func() *TransactionOwner { t := queried(); t.ReadOnly = true; return t }, []parser.TransactionMode{readWrite}, "transaction read-write mode must be set before any query"},
		{"deferrable in subtransaction", nested, []parser.TransactionMode{deferrable}, "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction"},
		{"deferrable after query", queried, []parser.TransactionMode{deferrable}, "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
		{"later mode fails", queried, []parser.TransactionMode{readOnly, deferrable}, "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := tc.owner()
			before := [3]any{owner.Isolation, owner.ReadOnly, owner.Deferrable}
			var sqlErr *sqlstate.Error
			if err := owner.AssignModes(tc.modes); !errors.As(err, &sqlErr) || sqlErr.Code != "25001" || sqlErr.Message != tc.message {
				t.Fatalf("AssignModes = %v, want 25001 %q", err, tc.message)
			}
			if after := [3]any{owner.Isolation, owner.ReadOnly, owner.Deferrable}; after != before {
				t.Fatalf("failed AssignModes changed the owner: %v, want %v", after, before)
			}
		})
	}

	// The current isolation may be repeated after the first query; every
	// valid mode applies in order.
	owner := &TransactionOwner{SnapshotSet: true, Isolation: Isolation(parser.IsolationSerializable)}
	if err := owner.AssignModes([]parser.TransactionMode{serializable, readOnly}); err != nil || !owner.ReadOnly {
		t.Fatal("repeated isolation rejected or later mode lost", err)
	}
	owner = &TransactionOwner{ReadOnly: true}
	if err := owner.AssignModes([]parser.TransactionMode{serializable, readWrite, deferrable, readOnly}); err != nil ||
		owner.Isolation != Isolation(parser.IsolationSerializable) || !owner.ReadOnly || !owner.Deferrable {
		t.Fatal("valid modes not applied in order", err, owner.Isolation, owner.ReadOnly, owner.Deferrable)
	}
}

func TestCharacteristicModeMapping(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        parser.TransactionMode
	}{
		{"transaction_isolation", "repeatable read", parser.TransactionMode{Isolation: parser.IsolationRepeatableRead}},
		{"transaction_deferrable", "on", parser.TransactionMode{DeferrableSet: true, Deferrable: true}},
		{"transaction_deferrable", "off", parser.TransactionMode{DeferrableSet: true}},
		{"transaction_read_only", "on", parser.TransactionMode{AccessSet: true, ReadOnly: true}},
		{"transaction_read_only", "off", parser.TransactionMode{AccessSet: true}},
	} {
		if got := CharacteristicMode(tc.name, tc.value); got != tc.want {
			t.Errorf("CharacteristicMode(%q, %q) = %+v, want %+v", tc.name, tc.value, got, tc.want)
		}
	}
	owner := &TransactionOwner{Isolation: Isolation(parser.IsolationRepeatableRead), Deferrable: true}
	for name, want := range map[string]string{"transaction_isolation": "repeatable read", "transaction_deferrable": "on", "transaction_read_only": "off"} {
		if got := owner.Characteristic(name); got != want {
			t.Errorf("Characteristic(%q) = %q, want %q", name, got, want)
		}
	}
	owner.Deferrable, owner.ReadOnly = false, true
	if owner.Characteristic("transaction_deferrable") != "off" || owner.Characteristic("transaction_read_only") != "on" {
		t.Error("Characteristic did not follow the owner's modes")
	}
}
