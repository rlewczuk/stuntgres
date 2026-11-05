package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
)

// These test-only bridges expose actual retained owners, never replacement
// frames or a production inspection/collection interface.
var StatementMutationImage = statementMutationImage
var StatementMutationCounters = statementMutationCounters
var StatementWALBytes = statementWALBytes

func PortalStatementCounters(p *protocolPortal) statistics.StatementCounters {
	return p.statementStats.execCounters
}

func SourceReadRef(lease exec.SourceReadLease) *ids.ObjectRef {
	return &lease.(*sourceRead).ref
}

func SourceReadNode(lease exec.SourceReadLease) *ids.OID {
	return &lease.(*sourceRead).node
}

func SourceReadStorageAddress(lease exec.SourceReadLease) *ids.ObjectAddress {
	return &lease.(*sourceRead).storageAddress
}

func SourceReadGeneration(lease exec.SourceReadLease) uint64 {
	return lease.(*sourceRead).generation
}

func PortalStatementDetached(p *protocolPortal) bool {
	return p.statementStats == nil || p.statementStats.parent == nil
}

func ScopeStatementCounters(s *StatementScope) statistics.StatementCounters {
	if s.statementStats == nil {
		return statistics.StatementCounters{}
	}
	return s.statementStats.execCounters
}
