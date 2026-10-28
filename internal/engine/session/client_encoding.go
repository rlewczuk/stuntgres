package session

import (
	"sync"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/pgencoding18"
)

// One newest successfully admitted pair per real encoding ID. This is not pair
// authority: only core's catalog resolution can populate it. The separate leaf
// mutex permits detached invocation stores to share the physical cache without
// taking the physical settings mutex under their own settings/session locks.
type clientEncodingCache struct {
	pairs [pgencoding18.Count]sqltype.ClientEncodingPolicy
}
type clientEncodingState struct {
	mu    sync.Mutex
	cache *clientEncodingCache
}

func (s *SettingsStore) clientEncodingAuthority() *SettingsStore {
	if s.clientEncodingOwner != nil {
		return s.clientEncodingOwner
	}
	return s
}

// AcceptClientEncodingLocked follows successful publication under the effective
// store's mutex and Session.Mu. Failed/canceled candidates never enter this cache.
func (s *SettingsStore) AcceptClientEncodingLocked(policy sqltype.ClientEncodingPolicy) {
	id := policy.ID()
	if id == 0 || id == 6 {
		return
	}
	owner := s.clientEncodingAuthority()
	owner.clientEncoding.mu.Lock()
	defer owner.clientEncoding.mu.Unlock()
	if owner.clientEncoding.cache == nil {
		owner.clientEncoding.cache = &clientEncodingCache{}
	}
	owner.clientEncoding.cache.pairs[id] = policy
}
func (s *SettingsStore) clientEncodingLocked() sqltype.ClientEncodingPolicy {
	id := s.TypedValue("client_encoding").ClientEncoding
	if id == 0 || id == 6 {
		p, _ := sqltype.NewClientEncodingPolicy(id, sqltype.EncodingConversionHandler{}, sqltype.EncodingConversionHandler{})
		return p
	}
	owner := s.clientEncodingAuthority()
	owner.clientEncoding.mu.Lock()
	defer owner.clientEncoding.mu.Unlock()
	if owner.clientEncoding.cache == nil {
		panic("unadmitted client encoding")
	}
	return owner.clientEncoding.cache.pairs[id]
}

// ClientEncoding captures immutable values only; it never resolves catalog
// defaults. Journal/reset restoration uses the newest accepted pair for its ID.
func (s *SettingsStore) ClientEncoding() sqltype.ClientEncodingPolicy {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.clientEncodingLocked()
}
