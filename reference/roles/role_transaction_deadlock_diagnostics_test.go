package reference_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func roleTransactionNativeDeadlockDetail(t *testing.T, detail string, roles []int64, relation int64, sessions []int) {
	t.Helper()
	lines := strings.Split(detail, "\n")
	if len(lines) != 2 {
		t.Fatalf("two-edge native cycle: %q", detail)
	}
	pattern := regexp.MustCompile(`^Transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\) waits for (ShareUpdateExclusiveLock|AccessShareLock) on (role|relation) ([1-9][0-9]*) \(class ([1-9][0-9]*), sub-ID 0, epoch ([1-9][0-9]*)\); blocked by transaction ([1-9][0-9]*) \(session ([1-9][0-9]*), epoch ([1-9][0-9]*)\)\.$`)
	a, b := pattern.FindStringSubmatch(lines[0]), pattern.FindStringSubmatch(lines[1])
	if a == nil || b == nil {
		t.Fatalf("native role/relation edge grammar: %q", detail)
	}
	if a[1] != b[9] || a[2] != b[10] || a[3] != b[11] || b[1] != a[9] || b[2] != a[10] || b[3] != a[11] || a[2] == b[2] {
		t.Fatalf("closed exact transaction/session/epoch cycle: %q", detail)
	}
	seenRoles := map[int64]bool{}
	relations := 0
	for _, edge := range [][]string{a, b} {
		sid, _ := strconv.Atoi(edge[2])
		if sid != sessions[0] && sid != sessions[1] {
			t.Fatalf("unrelated session in cycle: %q", detail)
		}
		oid, err := strconv.ParseInt(edge[6], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if edge[5] == "role" {
			if edge[4] != "ShareUpdateExclusiveLock" || edge[7] != "1260" {
				t.Fatalf("role mode/class: %q", detail)
			}
			found := false
			for _, role := range roles {
				if oid == role {
					found = true
				}
			}
			if !found || seenRoles[oid] {
				t.Fatalf("unexpected/repeated role resource: %q", detail)
			}
			seenRoles[oid] = true
			// A shared role epoch is genuine but independent of either database owner epoch.
		} else {
			if relation == 0 || oid != relation || edge[4] != "AccessShareLock" || edge[7] != "1259" || edge[8] != edge[3] {
				t.Fatalf("relation resource identity: %q", detail)
			}
			relations++
		}
	}
	wantRelations := 0
	if relation != 0 {
		wantRelations = 1
	}
	if len(seenRoles) != len(roles) || relations != wantRelations {
		t.Fatalf("complete expected resource cycle: %q", detail)
	}
}
