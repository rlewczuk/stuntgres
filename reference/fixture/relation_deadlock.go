package fixture

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func DeadlockPublicError(t *testing.T, err error, position string) *stuntgres.Error {
	t.Helper()
	var d *stuntgres.Error
	if !errors.As(err, &d) {
		t.Fatalf("want 40P01, got %v", err)
	}
	if d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Code != "40P01" || d.Message != "deadlock detected" || d.Hint != "See server log for query details." || d.Position != position || d.Context != "" {
		t.Fatalf("deadlock diagnostic %#v, expected position %q", d, position)
	}
	if d.InternalPosition != "" || d.InternalQuery != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" || d.File != "" || d.Line != "" || d.Routine != "" {
		t.Fatalf("unexpected unrelated native fields %#v", d)
	}
	State(t, err, "40P01")
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != 2 {
		t.Fatalf("two-edge detail: %q", d.Detail)
	}
	completion := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for transaction completion; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	relation := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for AccessExclusiveLock on relation ([1-9][0-9]*) \(class 1259, sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	for i, line := range lines {
		if c := completion.FindStringSubmatch(line); c != nil {
			r := relation.FindStringSubmatch(lines[1-i])
			if r == nil || c[1] != r[6] || c[2] != r[7] || c[3] != r[8] || c[4] != r[1] || c[5] != r[2] || c[6] != r[3] || r[5] != r[3] || c[2] == c[5] {
				t.Fatalf("complete completion/relation cycle identity: %q", d.Detail)
			}
			return d
		}
	}
	pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for (AccessShareLock|RowShareLock|RowExclusiveLock|ShareUpdateExclusiveLock|ShareRowExclusiveLock|AccessExclusiveLock|Update|NoKeyUpdate|Share|KeyShare) on relation ([1-9][0-9]*) \(class ([1-9][0-9]*), sub-ID 0, epoch ([1-9][0-9]*)\)( row ([1-9][0-9]*))?; blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	a := pattern.FindStringSubmatch(lines[0])
	b := pattern.FindStringSubmatch(lines[1])
	if a == nil || b == nil || a[1] != b[10] || b[1] != a[10] || a[2] != b[11] || b[2] != a[11] || a[3] != b[12] || b[3] != a[12] {
		t.Fatalf("complete closed native cycle identity: %q", d.Detail)
	}
	for _, edge := range [][]string{a, b} {
		if edge[6] != "1259" || edge[7] != edge[3] || (edge[4] == "Update" || edge[4] == "NoKeyUpdate" || edge[4] == "Share" || edge[4] == "KeyShare") != (edge[9] != "") {
			t.Fatalf("resource class/epoch/row-mode identity: %q", d.Detail)
		}
	}
	return d
}

func DeadlockPhysical(t *testing.T, err error, pids []int, relations map[int64]bool, database int64, xids map[string]int, victim int, kind, deletedTuple string) {
	t.Helper()
	d := ExtractDiagnostic(t, err)
	if d.Code != "40P01" || d.Message != "deadlock detected" || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" || d.Hint != "See server log for query details." || d.Position != "" || d.InternalPosition != "" || d.InternalQuery != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" {
		t.Fatalf("physical diagnostic %#v", d)
	}
	State(t, err, "40P01")
	lines := strings.Split(strings.TrimSpace(d.Detail), "\n")
	if len(lines) != 2 {
		t.Fatalf("two cycle edges: %q", d.Detail)
	}
	pattern := regexp.MustCompile(`^Process ([1-9][0-9]*) waits for (AccessExclusiveLock|ShareLock) on (relation ([1-9][0-9]*) of database ([1-9][0-9]*)|(?:virtual )?transaction ([1-9][0-9]*(?:/[1-9][0-9]*)?|0/[1-9][0-9]*)); blocked by process ([1-9][0-9]*)\.$`)
	edges := map[int]int{}
	relationEdges, rowEdges := 0, 0
	for _, line := range lines {
		m := pattern.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("complete physical edge: %q", line)
		}
		from, _ := strconv.Atoi(m[1])
		to, _ := strconv.Atoi(m[7])
		if from == to || !(from == pids[0] || from == pids[1]) || !(to == pids[0] || to == pids[1]) {
			t.Fatalf("participant PID edge: %q", line)
		}
		edges[from] = to
		if m[4] != "" {
			oid, _ := strconv.ParseInt(m[4], 10, 64)
			db, _ := strconv.ParseInt(m[5], 10, 64)
			if !relations[oid] || db != database || m[2] != "AccessExclusiveLock" {
				t.Fatalf("actual relation/db/mode: %q", line)
			}
			relationEdges++
		} else {
			if xids[m[6]] != to || m[2] != "ShareLock" {
				t.Fatalf("actual blocker transaction/mode: %q", line)
			}
			rowEdges++
		}
	}
	if len(edges) != 2 || edges[pids[0]] != pids[1] || edges[pids[1]] != pids[0] {
		t.Fatalf("unclosed PID cycle: %q", d.Detail)
	}
	switch kind {
	case "table":
		if relationEdges != 2 || d.Context != "" {
			t.Fatalf("table diagnostic %#v", d)
		}
	case "mixed", "completion":
		if relationEdges != 1 || rowEdges != 1 {
			t.Fatalf("mixed edge kinds: %q", d.Detail)
		}
	default:
		if rowEdges != 2 {
			t.Fatalf("row edge kinds: %q", d.Detail)
		}
	}
	rowVictim := kind == "rows" || kind == "delete" || kind == "partial" || kind == "mixed" && victim == 1
	if rowVictim {
		verb := "updating"
		if kind == "delete" && victim == 1 {
			verb = "deleting"
		}
		pattern := regexp.MustCompile(`^while ` + verb + ` tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`)
		if !pattern.MatchString(d.Context) {
			t.Fatalf("physical tuple context %q", d.Context)
		}
	} else if d.Context != "" {
		t.Fatalf("utility context %q", d.Context)
	}
	if kind == "delete" && victim == 1 && d.Context != fmt.Sprintf("while deleting tuple %s in relation \"t\"", deletedTuple) {
		t.Fatalf("captured pre-deletion physical tuple: %q", d.Context)
	}
}
