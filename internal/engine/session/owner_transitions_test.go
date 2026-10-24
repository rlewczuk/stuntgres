package session

import (
	"strings"
	"testing"
)

func TestOwnerChainModes(t *testing.T) {
	owner := &TransactionOwner{State: TxActive, Isolation: Serializable, ReadOnly: true, Deferrable: true, Savepoints: []SavepointMark{{ReadOnly: false}, {ReadOnly: true}}}
	if modes := owner.ChainModes(true); modes != (TxOptions{Isolation: Serializable, ReadOnly: true, Deferrable: true}) {
		t.Fatal("healthy commit carries the current child mode", modes)
	}
	if modes := owner.ChainModes(false); modes.ReadOnly || modes.Isolation != Serializable || !modes.Deferrable {
		t.Fatal("rollback takes the outermost access mode", modes)
	}
	owner.State = TxAborted
	if modes := owner.ChainModes(true); modes.ReadOnly {
		t.Fatal("failed commit takes the outermost access mode", modes)
	}
	owner.Savepoints = nil
	if modes := owner.ChainModes(false); !modes.ReadOnly {
		t.Fatal("without savepoints rollback keeps the block mode", modes)
	}
}

func TestOwnerPrepareRefusalOrder(t *testing.T) {
	long := string(make([]byte, 200))
	owner := &TransactionOwner{AccessedTemporary: true, NotificationEvents: []Notification{{}}}
	for _, tc := range []struct {
		gid             string
		hold, foreign   bool
		code, substring string
	}{
		{long, true, true, "0A000", "WITH HOLD"},
		{long, false, true, "22023", "too long"},
		{"g", false, true, "0A000", "temporary objects"},
	} {
		err := owner.PrepareRefusal(tc.gid, tc.hold, tc.foreign)
		if preparedRegistryCode(err) != tc.code || !strings.Contains(err.Error(), tc.substring) {
			t.Fatal(tc.substring, err)
		}
	}
	owner.AccessedTemporary = false
	if err := owner.PrepareRefusal("g", false, true); !strings.Contains(err.Error(), "foreign tables") {
		t.Fatal("foreign", err)
	}
	if err := owner.PrepareRefusal("g", false, false); !strings.Contains(err.Error(), "LISTEN") {
		t.Fatal("notifications", err)
	}
	owner.NotificationEvents = nil
	if err := owner.PrepareRefusal("g", false, false); err != nil {
		t.Fatal("eligible", err)
	}
}
