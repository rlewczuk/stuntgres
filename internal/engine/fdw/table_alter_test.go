package fdw

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// TestFDWAlterCandidate pins the working copy of ALTER FOREIGN TABLE: option
// changes on it never reach the published relation, and a missing column is
// 42703.
func TestFDWAlterCandidate(t *testing.T) {
	old := &catalog.Relation{
		Index:      map[string]int{"a": 0},
		Attributes: []int32{1},
		Foreign: &catalog.ForeignTableDefinition{
			Options: []catalog.ForeignOption{{Name: "t", Value: "1"}},
			Columns: map[int32][]catalog.ForeignOption{1: {{Name: "c", Value: "1"}}},
		},
	}
	next := AlterCandidate(old)
	next.Foreign.Options = nil
	next.Foreign.Columns[1] = nil
	if len(old.Foreign.Options) != 1 || len(old.Foreign.Columns[1]) != 1 {
		t.Fatal("candidate shares the published options", old.Foreign)
	}
	if slot, err := AlterColumn(&next, "a"); err != nil || slot != 0 {
		t.Fatal("existing column", slot, err)
	}
	_, err := AlterColumn(&next, "missing")
	if d, ok := err.(*sqlstate.Error); !ok || d.Code != "42703" {
		t.Fatal("missing column", err)
	}
}
