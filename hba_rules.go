package stuntgres

import "stuntgres/internal/engine/hbarules"

// HBARule is one simulated pg_hba_file_rules diagnostic row. Each field is
// independent: nil scalars/arrays are SQL NULL; nonnil empty arrays are '{}'.
// FileName is a label, not filesystem authority. Rules never authenticate.
type HBARule = hbarules.Rule

// SetHBARules replaces this Driver's diagnostic rules across all exact DSNs and
// logical databases, before or after connectors are opened. Nil or empty clears.
// It copies all rules, pointees and arrays before one immutable publication;
// callers may mutate their inputs after return, but not concurrently with Set.
// Existing results stay detached; each new execution captures the latest batch.
// There is no validation, normalization, file I/O or authentication effect.
func (d *Driver) SetHBARules(rules []HBARule) {
	d.mu.Lock()
	if d.hbaRules == nil {
		d.hbaRules = new(hbarules.Source)
	}
	source := d.hbaRules
	d.mu.Unlock()
	source.Set(rules)
}
