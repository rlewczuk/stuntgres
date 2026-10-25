package engine

import (
	"stuntgres/internal/engine/authorization"
)

// Caller holds the cluster metadata mutex and this runtime's sessionMu.
// Admission across targets is serialized; teardown never takes the metadata
// mutex while holding sessionMu. Session identities are immutable here.
func (e *Engine) roleSessionCountLocked(ref authorization.PrincipalRef) int {
	n := 0
	for _, s := range e.sessions {
		if s.CountedAuthenticated == ref {
			n++
		}
	}
	if e.Cluster != nil {
		for _, target := range e.Cluster.Targets {
			if target == e {
				continue
			}
			target.sessionMu.Lock()
			for _, s := range target.sessions {
				if s.CountedAuthenticated == ref {
					n++
				}
			}
			target.sessionMu.Unlock()
		}
	}
	return n
}
