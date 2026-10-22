package pgcatalog

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/ids"
)

func TestInfoRoutineSequenceUsageSpecificName(t *testing.T) {
	for _, tc := range []struct {
		name string
		oid  ids.OID
		want string
	}{
		{"short", 42, "short_42"},
		{strings.Repeat("a", 63), 42, strings.Repeat("a", 60) + "_42"},
		{strings.Repeat("a", 61) + "é", 42, strings.Repeat("a", 60) + "_42"},
		{strings.Repeat("é", 31), 4294967295, strings.Repeat("é", 26) + "_4294967295"},
		{"", 42, "_42"},
	} {
		got := routineSequenceSpecificName(tc.name, tc.oid)
		if got != tc.want || len(got) > 63 || !utf8.ValidString(got) {
			t.Fatalf("name=%q oid=%d got=%q want=%q", tc.name, tc.oid, got, tc.want)
		}
	}
}
