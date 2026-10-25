package analyze

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func TestQueryLockOccurrenceNormalization(t *testing.T) {
	for _, tc := range []struct {
		q     string
		modes []lock.Mode
		waits []parser.RowLockWait
	}{
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR UPDATE OF a,a SKIP LOCKED FOR SHARE OF a NOWAIT", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockNowait, parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR SHARE OF a NOWAIT FOR UPDATE OF a SKIP LOCKED", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockNowait, parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR SHARE OF a FOR KEY SHARE OF a SKIP LOCKED FOR UPDATE OF b NOWAIT", []lock.Mode{lock.ForShare, lock.ForUpdate}, []parser.RowLockWait{parser.RowLockSkip, parser.RowLockNowait}},
		{"SELECT j.id FROM (t JOIN u USING(id)) j FOR UPDATE OF t", []lock.Mode{lock.ForUpdate, 0}, []parser.RowLockWait{parser.RowLockBlock, parser.RowLockBlock}},
		{"SELECT x.id FROM t CROSS JOIN (VALUES(1)) x(id) FOR SHARE OF x", []lock.Mode{0}, []parser.RowLockWait{parser.RowLockBlock}},
		{"SELECT a.id FROM t a JOIN t b USING(id) FOR KEY SHARE FOR UPDATE OF b SKIP LOCKED", []lock.Mode{lock.ForKeyShare, lock.ForUpdate}, []parser.RowLockWait{parser.RowLockBlock, parser.RowLockSkip}},
	} {
		raw := statement(t, tc.q).(*parser.Select)
		marks, err := RowLockTargets(raw.From, raw.Locking)
		if err != nil || len(marks) != len(tc.modes) {
			t.Fatal(tc.q, marks, err)
		}
		for i, mark := range marks {
			if mark.Mode != tc.modes[i] || mark.Wait != tc.waits[i] {
				t.Fatal(tc.q, i, mark)
			}
		}
		again, err := RowLockTargets(raw.From, raw.Locking)
		if err != nil || !reflect.DeepEqual(marks, again) {
			t.Fatal("normalization mutated original clauses", marks, again, err)
		}
	}
	raw := statement(t, "SELECT id FROM t").(*parser.Select)
	if marks, err := RowLockTargets(raw.From, raw.Locking); marks != nil || err != nil {
		t.Fatal("ordinary source gained manifest", marks, err)
	}
}

func statement(t *testing.T, sql string) parser.Statement {
	t.Helper()
	s, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("expected single statement, got %d", len(s))
	}
	return s[0]
}
