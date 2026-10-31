package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"sync"
	"time"
)

// Session.Mu owns scheduling/request state. Transaction admission still owns the
// writer: the callback only tries admission, never waits in inverted lock order.
// This is PostgreSQL's one-shot idle timeout, not a polling background worker.
type statsFlushSlot struct {
	timer      *time.Timer
	generation uint64
	deadline   time.Time
	requested  bool
	callbacks  sync.WaitGroup
}

func (slot *statsFlushSlot) stop() {
	slot.generation++
	if slot.timer != nil {
		if slot.timer.Stop() {
			slot.callbacks.Done()
		}
		slot.timer = nil
	}
	slot.deadline = time.Time{}
	slot.requested = false
}
func statsOutsideTransaction(s *session.Session) bool {
	return s.Transactions != nil && s.Transactions.State == session.TxIdle && !s.Transactions.RetainedMessage
}

// Called under admission and Session.Mu at a genuine completed command boundary.
func statsReportIdleLocked(s *session.Session, now time.Time) {
	p := PayloadOf(s)
	if s.State != session.Live || !statsOutsideTransaction(s) {
		return
	}
	if p.statsWriter.Pending() {
		p.statsWriter.Flush(&EngineOf(s).Statistics, now, p.statsFlush.requested)
	}
	if !p.statsWriter.Pending() {
		if p.statsFlush.timer != nil || p.statsFlush.requested {
			p.statsFlush.stop()
		}
		return
	}
	slot := &p.statsFlush
	if slot.timer != nil || slot.requested {
		return
	}
	slot.generation++
	slot.deadline = now.Add(10 * time.Second)
	generation, token := slot.generation, s.SessionToken
	slot.callbacks.Add(1)
	slot.timer = time.AfterFunc(time.Until(slot.deadline), func() {
		defer slot.callbacks.Done()
		s.Mu.Lock()
		defer s.Mu.Unlock()
		statsFlushExpiredLocked(s, generation, token, time.Now())
	})
}

func statsFlushExpiredLocked(s *session.Session, generation uint64, token ids.SessionToken, now time.Time) {
	p := PayloadOf(s)
	slot := &p.statsFlush
	if s.State != session.Live || s.SessionToken != token || slot.generation != generation || slot.deadline.IsZero() || now.Before(slot.deadline) {
		return
	}
	slot.timer = nil
	slot.deadline = time.Time{}
	slot.generation++
	slot.requested = true
	statsReadyLocked(s, now)
}

// The existing SQL-lease ready projection guards activity, while a nonblocking
// admission acquisition guards the actual owner state and reportable writer.
// Due work survives active/retained/transaction blocks until their next true idle.
func statsReadyLocked(s *session.Session, now time.Time) {
	p := PayloadOf(s)
	if p == nil || !p.statsFlush.requested || s.State != session.Live || s.Timeouts.SQLLeases != 0 || s.Transactions == nil || !s.Transactions.Admission.TryLock() {
		return
	}
	defer s.Transactions.Admission.Unlock()
	if !s.Transactions.Mu.TryLock() {
		return
	}
	defer s.Transactions.Mu.Unlock()
	if statsOutsideTransaction(s) {
		statsReportIdleLocked(s, now)
	}
}

// DrainStatistics calls this after all resource callbacks and outside Session.Mu.
// Ending prevents new arms; invalidation precedes joining callbacks that may
// already be waiting for Session.Mu. No lifecycle/admission lock spans Wait.
func cancelStatsFlush(s *session.Session) {
	p := PayloadOf(s)
	if p == nil {
		return
	}
	s.Mu.Lock()
	p.statsFlush.stop()
	s.Mu.Unlock()
	p.statsFlush.callbacks.Wait()
}
