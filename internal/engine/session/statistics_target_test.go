package session

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestStatisticsNDistinctCommandTag(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		statement := &parser.AlterStatisticsTarget{IfExists: conditional}
		tag, counted, insert, ok := ClassifyCommandTag(statement, 0)
		if !ok || tag != "ALTER STATISTICS" || counted || insert {
			t.Fatalf("target command tag: %q counted=%v insert=%v admitted=%v", tag, counted, insert, ok)
		}
		if tag, eligible := EventCommand(statement); !eligible || tag != "ALTER STATISTICS" {
			t.Fatalf("target event classification: %q eligible=%v", tag, eligible)
		}
	}
}
