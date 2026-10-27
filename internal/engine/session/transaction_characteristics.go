package session

import "stuntgres/internal/parser"

// AssignModes validates modes in order against t's snapshot and savepoint
// state and, when every mode is valid, installs the resulting isolation,
// access and deferrable modes. A failure changes nothing. The caller holds
// t.Mu and publishes the modes to the settings store afterwards.
func (t *TransactionOwner) AssignModes(modes []parser.TransactionMode) error {
	isolation, readOnly, deferrable, err := t.evaluateModes(modes, t.SnapshotSet)
	if err != nil {
		return err
	}
	t.Isolation, t.ReadOnly, t.Deferrable = isolation, readOnly, deferrable
	return nil
}

// CheckModes validates without installing modes. snapshotSet includes a
// caller-owned utility snapshot that is not retained on the transaction owner.
// The caller serializes owner access through transaction admission or t.Mu.
func (t *TransactionOwner) CheckModes(modes []parser.TransactionMode, snapshotSet bool) error {
	_, _, _, err := t.evaluateModes(modes, t.SnapshotSet || snapshotSet)
	return err
}

func (t *TransactionOwner) evaluateModes(modes []parser.TransactionMode, snapshotSet bool) (Isolation, bool, bool, error) {
	isolation, readOnly, deferrable := t.Isolation, t.ReadOnly, t.Deferrable
	for _, mode := range modes {
		if mode.Isolation != parser.IsolationDefault {
			next := Isolation(mode.Isolation)
			if snapshotSet && next != isolation {
				return isolation, readOnly, deferrable, sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
			}
			if len(t.Savepoints) != 0 && next != isolation {
				return isolation, readOnly, deferrable, sqlError("25001", "SET TRANSACTION ISOLATION LEVEL must not be called in a subtransaction")
			}
			isolation = next
		}
		if mode.AccessSet {
			if len(t.Savepoints) != 0 && readOnly && !mode.ReadOnly {
				return isolation, readOnly, deferrable, sqlError("25001", "cannot set transaction read-write mode inside a read-only transaction")
			}
			if snapshotSet && readOnly && !mode.ReadOnly {
				return isolation, readOnly, deferrable, sqlError("25001", "transaction read-write mode must be set before any query")
			}
			readOnly = mode.ReadOnly
		}
		if mode.DeferrableSet {
			if len(t.Savepoints) != 0 {
				return isolation, readOnly, deferrable, sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction")
			}
			if snapshotSet {
				return isolation, readOnly, deferrable, sqlError("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query")
			}
			deferrable = mode.Deferrable
		}
	}
	return isolation, readOnly, deferrable, nil
}

// CharacteristicMode maps a SET of one of the current transaction
// characteristics (transaction_isolation, transaction_deferrable, otherwise
// transaction_read_only) with its formatted value to a transaction mode.
func CharacteristicMode(name, value string) parser.TransactionMode {
	mode := parser.TransactionMode{}
	if name == "transaction_isolation" {
		mode.Isolation = parser.TransactionIsolation(IsolationFromText(value))
	} else if name == "transaction_deferrable" {
		mode.DeferrableSet, mode.Deferrable = true, value == "on"
	} else {
		mode.AccessSet = true
		mode.ReadOnly = value == "on"
	}
	return mode
}

// SessionCharacteristicSetting maps one SET SESSION CHARACTERISTICS mode to
// its default_transaction_* setting and value; the last mode kind set wins.
func SessionCharacteristicSetting(mode parser.TransactionMode) (name, value string) {
	if mode.Isolation != parser.IsolationDefault {
		name, value = "default_transaction_isolation", IsolationText(Isolation(mode.Isolation))
	}
	if mode.AccessSet {
		name, value = "default_transaction_read_only", "off"
		if mode.ReadOnly {
			value = "on"
		}
	}
	if mode.DeferrableSet {
		name, value = "default_transaction_deferrable", "off"
		if mode.Deferrable {
			value = "on"
		}
	}
	return name, value
}

// Characteristic returns the setting text of t's current transaction
// characteristic name, as CharacteristicMode names them.
func (t *TransactionOwner) Characteristic(name string) string {
	if name == "transaction_isolation" {
		return IsolationText(t.Isolation)
	}
	if name == "transaction_deferrable" {
		if t.Deferrable {
			return "on"
		}
		return "off"
	}
	if t.ReadOnly {
		return "on"
	}
	return "off"
}
