package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func ScopeEndCopyProgress(s *StatementScope) { s.endCopyProgress(s.copyProgress) }

type CopyProgressEntry = copyProgress

func ScopeBeginCopyProgress(s *StatementScope, relation ids.OID, from bool) *CopyProgressEntry {
	return s.beginCopyProgress(relation, from)
}
func ScopeEndCopyProgressEntry(s *StatementScope, p *CopyProgressEntry) { s.endCopyProgress(p) }
func ScopeCopyProgressProbe(s *StatementScope) func() pgcatalog.CopyProgressFact {
	p := s.copyProgress
	return func() pgcatalog.CopyProgressFact {
		return pgcatalog.CopyProgressFact{BytesProcessed: p.bytes.Load(), TuplesProcessed: p.tuples.Processed.Load(), TuplesExcluded: p.tuples.Excluded.Load(), TuplesSkipped: p.tuples.Skipped.Load()}
	}
}
