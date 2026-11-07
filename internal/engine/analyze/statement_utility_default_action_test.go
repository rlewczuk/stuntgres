package analyze

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestJumbleAlterDefaultListAction(t *testing.T) {
	queryID := func(q string) int64 {
		t.Helper()
		statements, err := parser.Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		jumble, _ := JumbleUtility(statements[0])
		return jumble.QueryID()
	}
	list := "ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b SET DEFAULT 2"
	if first, second := queryID(list), queryID(list); first != second {
		t.Fatal("identical default lists differ", first, second)
	}
	for _, pair := range [][2]string{
		{"ALTER TABLE t ALTER COLUMN a SET DEFAULT a, ALTER COLUMN b SET DEFAULT 2", "ALTER TABLE t ALTER COLUMN a SET DEFAULT a+1, ALTER COLUMN b SET DEFAULT 2"},
		{"ALTER TABLE t ALTER COLUMN a SET DEFAULT 1, ALTER COLUMN b SET DEFAULT 2", "ALTER TABLE t ALTER COLUMN a DROP DEFAULT, ALTER COLUMN b SET DEFAULT 2"},
		{"ALTER TABLE t ALTER i SET DEFAULT 7", "ALTER TABLE t ALTER i SET DEFAULT 8"},
	} {
		if queryID(pair[0]) == queryID(pair[1]) {
			t.Fatal("structural change kept query id", pair)
		}
	}
}
