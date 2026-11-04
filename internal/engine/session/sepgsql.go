package session

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
)

// SepgsqlSubject is a physical-session resource, independent of SQL roles. Its
// mutex is below Engine/Settings/Session locks and never calls another owner.
// Statement admission serializes mutation and lexical trusted frames.
type SepgsqlSubject struct {
	mu                           sync.Mutex
	token                        ids.SessionToken
	policy                       *sepgsql.Runtime
	original, committed, pending string
	generation                   uint64
	lexical                      uint64
	trusted                      []sepgsqlTrustedFrame
	ended                        bool
}
type sepgsqlTrustedFrame struct {
	context string
	stamp   uint64
}
type SepgsqlMark struct{ pending string }

func NewSepgsqlSubject(token ids.SessionToken, policy *sepgsql.Runtime, context string) (*SepgsqlSubject, error) {
	if !policy.Valid(context) {
		return nil, fmt.Errorf("stuntgres: invalid simulated sepgsql connection context")
	}
	return &SepgsqlSubject{token: token, policy: policy, original: context, committed: context, pending: context, generation: 1}, nil
}
func (s *SepgsqlSubject) Current() string { context, _, _, _ := s.PhaseSnapshot(); return context }
func (s *SepgsqlSubject) Snapshot() (string, uint64) {
	context, generation, _, _ := s.PhaseSnapshot()
	return context, generation
}

// PhaseSnapshot distinguishes transient lexical ownership from transactional
// subject generation. Balanced trusted return restores the parent's stamp.
func (s *SepgsqlSubject) PhaseSnapshot() (string, uint64, uint64, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.trusted); n != 0 {
		frame := s.trusted[n-1]
		return frame.context, s.generation, frame.stamp, n
	}
	return s.pending, s.generation, 0, 0
}
func (s *SepgsqlSubject) Original() string   { s.mu.Lock(); defer s.mu.Unlock(); return s.original }
func (s *SepgsqlSubject) Generation() uint64 { s.mu.Lock(); defer s.mu.Unlock(); return s.generation }
func (s *SepgsqlSubject) Set(context string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = context
	s.generation++
}
func (s *SepgsqlSubject) Mark() SepgsqlMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SepgsqlMark{s.pending}
}
func (s *SepgsqlSubject) Restore(mark SepgsqlMark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = mark.pending
	s.generation++
}
func (s *SepgsqlSubject) Finish(committed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if committed {
		s.committed = s.pending
	} else {
		s.pending = s.committed
	}
	s.generation++
}
func (s *SepgsqlSubject) Enter(context string) sqltype.RoutineBodyToken {
	s.mu.Lock()
	depth := len(s.trusted)
	s.lexical++
	stamp := s.lexical
	s.trusted = append(s.trusted, sepgsqlTrustedFrame{context: context, stamp: stamp})
	s.mu.Unlock()
	return sqltype.NewRoutineBodyToken(s, depth, stamp)
}
func (s *SepgsqlSubject) LeaveRoutineBody(depth int, stamp uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	if len(s.trusted) != depth+1 || s.trusted[depth].stamp != stamp {
		panic("stuntgres: unbalanced sepgsql trusted frame")
	}
	s.trusted = s.trusted[:depth]
}
func (s *SepgsqlSubject) Push(context string) func() {
	token := s.Enter(context)
	return token.Leave
}
func (s *SepgsqlSubject) EndSession(token ids.SessionToken, _ EndReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token != s.token {
		return fmt.Errorf("stuntgres: foreign sepgsql subject resource")
	}
	s.original, s.committed, s.pending = "", "", ""
	s.trusted = nil
	s.ended = true
	s.generation++
	return nil
}

type sepgsqlAuditDelivery struct {
	policy *sepgsql.Runtime
	event  sepgsql.AuditEvent
}

// SepgsqlAudit queues detached records during serial command execution, before
// Done. Host code runs only after the winning Done releases the lease and locks.
// LOG notice filtering is independent of the host audit selection.
func (op *Operation) SepgsqlAudit(policy *sepgsql.Runtime, event sepgsql.AuditEvent) {
	op.sepgsqlAudit = append(op.sepgsqlAudit, sepgsqlAuditDelivery{policy, event})
	op.EmitNotice(LevelLog, sqlstate.Error{Severity: "LOG", SeverityUnlocalized: "LOG", Code: "00000", Message: event.Message})
}
func deliverSepgsqlAudit(events []sepgsqlAuditDelivery) {
	for _, delivery := range events {
		delivery.policy.Deliver(delivery.event)
	}
}
