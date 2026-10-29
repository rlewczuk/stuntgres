package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RecordCandidateInitialPrivileges preserves predecessor observations across
// statements, including observed absence and a transaction's own tombstones.
func (c *Journal) RecordCandidateInitialPrivileges(base *catalog.State, candidate *catalog.Candidate) {
	for address, record := range candidate.InitialReads() {
		if _, private := c.InitialPrivileges[address]; private {
			continue
		}
		if c.InitialReads == nil {
			c.InitialReads = make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord)
		}
		if _, exists := c.InitialReads[address]; !exists {
			c.InitialReads[address] = record
		}
	}
	for address, record := range candidate.InitialPrivileges() {
		if c.InitialOriginals == nil {
			c.InitialOriginals = make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord)
			c.InitialPrivileges = make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord)
		}
		if _, exists := c.InitialOriginals[address]; !exists {
			c.InitialOriginals[address] = base.InitialPrivilege(address)
		}
		c.InitialPrivileges[address] = record
		c.CachedBase = nil
		c.Cached = catalog.Read{}
	}
}
