package session

import (
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestNoticesFilter(t *testing.T) {
	thresholds := []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"}
	ranks := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 3}
	witness := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "field witness", Detail: "facts", Hint: "advice", Position: "3", InternalPosition: "2", InternalQuery: "x+", Context: "context", Schema: "s", Table: "t", Column: "c", DataType: "d", Constraint: "k"}
	for i, threshold := range thresholds {
		t.Run(threshold, func(t *testing.T) {
			normalized, notice, err := settings.ClientMinMessages.Normalize(threshold)
			if err != nil || notice != nil {
				t.Fatal(normalized, notice, err)
			}
			level, _ := messageLevel(normalized)
			for emitted := 0; emitted <= 9; emitted++ {
				op := &Operation{NoticeThreshold: level}
				op.EmitNotice(NoticeLevel(emitted), witness)
				got := op.DetachNotices()
				want := emitted < 9 && (emitted == 6 || emitted >= ranks[i])
				if (len(got) == 1) != want {
					t.Fatalf("level %d got %v want delivery %v", emitted, got, want)
				}
				if want {
					if got[0] != witness {
						t.Fatal("fields changed")
					}
					got[0].Detail = "mutated"
					if witness.Detail != "facts" {
						t.Fatal("payload aliases")
					}
				}
				if op.DetachNotices() != nil {
					t.Fatal("not detached")
				}
			}
		})
	}
}
