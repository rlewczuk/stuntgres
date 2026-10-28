package session

import "stuntgres/internal/sqlstate"

// OutputNotices is detached delivery configuration, not session authority. A
// transport captures it during admission and uses a fresh collector only around
// actual typed output. Neither the operation nor its session is retained.
type OutputNotices struct {
	Handler   func(sqlstate.Error)
	Threshold NoticeLevel
}

func (d OutputNotices) Deliver(notices []sqlstate.Error) {
	if d.Handler == nil {
		return
	}
	for _, notice := range notices {
		if LevelWarning >= d.Threshold {
			d.Handler(notice)
		}
	}
}

// OutputNoticeCollector owns one synchronous conversion's records. It never
// invokes a transport callback; callers drain it only after releasing locks.
type OutputNoticeCollector struct{ notices []sqlstate.Error }

func (c *OutputNoticeCollector) Add(d sqlstate.Error) { c.notices = append(c.notices, d) }
func (c *OutputNoticeCollector) Detach() []sqlstate.Error {
	n := c.notices
	c.notices = nil
	return n
}
