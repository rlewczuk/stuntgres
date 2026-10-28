package enginetests

import (
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// These are syntax classifications, not a SQL fixture or a substitute for the
// committed-publication witnesses in partitioncmds and reference/partitions.
func TestPartitionDetachConcurrentClassification(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mode         parser.PartitionDetachMode
		early, event bool
	}{
		{"ordinary", parser.DetachOrdinary, false, true},
		{"concurrent", parser.DetachConcurrent, true, false},
		{"finalize", parser.DetachFinalize, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &parser.AlterTableDetachPartition{Mode: tc.mode}
			if got := session.EarlyStandaloneUtility(raw); got != tc.early {
				t.Fatalf("early standalone admission=%v, want %v", got, tc.early)
			}
			tag, event := session.EventCommand(raw)
			if tag != "ALTER TABLE" || event != tc.event {
				t.Fatalf("event classification=(%q,%v), want (ALTER TABLE,%v)", tag, event, tc.event)
			}
			if !session.TransactionalSchemaUtility(raw) {
				t.Fatal("detach lost schema utility binding/description classification")
			}
		})
	}
}
