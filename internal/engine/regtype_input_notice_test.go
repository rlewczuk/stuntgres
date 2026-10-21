package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

type typeNoticeRecorder struct {
	exec.Host
	levels  []session.NoticeLevel
	notices []sqlstate.Error
}

func (h *typeNoticeRecorder) TypeNameNotice(level session.NoticeLevel, notice sqlstate.Error) {
	h.levels = append(h.levels, level)
	h.notices = append(h.notices, notice)
}

type silentHost struct{ exec.Host }

// A resolver bound to an execution authority emits type-name notices to the
// host attached when it resolves, never to the one attached at capture, and
// emits nothing when that host takes no notices.
func TestTypeInputNoticesFollowTheCurrentHost(t *testing.T) {
	long := strings.Repeat("a", 70)
	first, second := &typeNoticeRecorder{}, &typeNoticeRecorder{}
	authority := &exec.Authority{Host: first}
	input := capturedRegtypeInput{authority: authority}.RegtypeParameter(2, false).(capturedRegtypeInput)
	resolve := func() {
		t.Helper()
		_, err := input.resolveTypeName(context.Background(), long)
		if d, ok := err.(*sqlstate.Error); !ok || d.Code != "42704" {
			t.Fatalf("resolve %q: %v", long, err)
		}
	}
	resolve()
	authority.Host = second
	resolve()
	authority.Host = silentHost{}
	resolve()
	authority.Host = nil
	resolve()
	if len(first.notices) != 1 || len(second.notices) != 1 {
		t.Fatalf("notices: first %d, second %d", len(first.notices), len(second.notices))
	}
	got := second.notices[0]
	wantContext := `invalid type name "` + long + `"` + "\nunnamed portal parameter $2 = '...'"
	if second.levels[0] != session.LevelNotice || got.Code != "42622" || got.Context != wantContext {
		t.Fatalf("notice = level %v %#v", second.levels[0], got)
	}
}
