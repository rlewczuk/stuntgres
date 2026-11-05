package statistics

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// StatementKey is cluster-wide. QueryID contains only the released selected
// query structure; principal, database and nesting are separate dimensions.
type StatementKey struct {
	UserID, DatabaseID uint32
	QueryID            int64
	TopLevel           bool
}

// StatementMoments accumulates actual milliseconds using Welford's recurrence.
// Extrema have their own sample count because minmax-only clear preserves totals.
type StatementMoments struct {
	Count                     int64
	Total, Min, Max, Mean, M2 float64
	extrema                   int64
}

func (m *StatementMoments) add(milliseconds float64) {
	m.Count++
	m.Total += milliseconds
	delta := milliseconds - m.Mean
	m.Mean += delta / float64(m.Count)
	m.M2 += delta * (milliseconds - m.Mean)
	if m.extrema == 0 || milliseconds < m.Min {
		m.Min = milliseconds
	}
	if m.extrema == 0 || milliseconds > m.Max {
		m.Max = milliseconds
	}
	m.extrema++
}
func (m StatementMoments) Stddev() float64 {
	if m.Count == 0 {
		return 0
	}
	return math.Sqrt(math.Max(0, m.M2/float64(m.Count)))
}
func (m *StatementMoments) clearExtrema() { m.Min, m.Max, m.extrema = 0, 0, 0 }

// StatementCounters is successful logical work, copied with its owning entry.
// Mechanisms without native producers remain zero; no runtime handles escape.
type StatementCounters struct {
	SharedHit, SharedRead, SharedDirtied, SharedWritten int64
	LocalHit, LocalRead, LocalDirtied, LocalWritten int64
	TempRead, TempWritten int64
	SharedReadTime, SharedWriteTime, LocalReadTime, LocalWriteTime float64
	TempReadTime, TempWriteTime float64
	WALRecords, WALFPI int64
	WALBytes uint64
	WALBuffersFull int64
	JITFunctions, JITInliningCount, JITOptimizationCount, JITEmissionCount, JITDeformCount int64
	JITGenerationTime, JITInliningTime, JITOptimizationTime, JITEmissionTime, JITDeformTime float64
	WorkersPlanned, WorkersLaunched int64
}

func (c *StatementCounters) Add(v StatementCounters) {
	c.SharedHit += v.SharedHit
	c.SharedRead += v.SharedRead
	c.SharedDirtied += v.SharedDirtied
	c.SharedWritten += v.SharedWritten
	c.LocalHit += v.LocalHit
	c.LocalRead += v.LocalRead
	c.LocalDirtied += v.LocalDirtied
	c.LocalWritten += v.LocalWritten
	c.TempRead += v.TempRead
	c.TempWritten += v.TempWritten
	c.SharedReadTime += v.SharedReadTime
	c.SharedWriteTime += v.SharedWriteTime
	c.LocalReadTime += v.LocalReadTime
	c.LocalWriteTime += v.LocalWriteTime
	c.TempReadTime += v.TempReadTime
	c.TempWriteTime += v.TempWriteTime
	c.WALRecords += v.WALRecords
	c.WALFPI += v.WALFPI
	c.WALBytes += v.WALBytes
	c.WALBuffersFull += v.WALBuffersFull
	c.JITFunctions += v.JITFunctions
	c.JITInliningCount += v.JITInliningCount
	c.JITOptimizationCount += v.JITOptimizationCount
	c.JITEmissionCount += v.JITEmissionCount
	c.JITDeformCount += v.JITDeformCount
	c.JITGenerationTime += v.JITGenerationTime
	c.JITInliningTime += v.JITInliningTime
	c.JITOptimizationTime += v.JITOptimizationTime
	c.JITEmissionTime += v.JITEmissionTime
	c.JITDeformTime += v.JITDeformTime
	c.WorkersPlanned += v.WorkersPlanned
	c.WorkersLaunched += v.WorkersLaunched
}

// StatementEntry is an owned snapshot; no live session, plan or executor escapes.
type StatementEntry struct {
	Key                          StatementKey
	Query                        string
	Planning, Execution          StatementMoments
	Rows                         int64
	Counters                     StatementCounters
	StatsSince, MinMaxStatsSince time.Time
}
type statementEntry struct {
	StatementEntry
	usage float64
}

// StatementToken names one live admission, independently of entry eviction.
// Release ends its eligibility on every successful or failed runtime terminal.
type StatementToken struct{ id uint64 }

// StatementRecord captures successful phase provenance outside the leaf lock.
// Query is cleaned raw source, used only when storage must allocate a new entry.
type StatementRecord struct {
	Key          StatementKey
	Query        string
	At           time.Time
	Milliseconds float64
	Rows         int64
	Counters     StatementCounters
}

type statementAdmission struct {
	keys    [2]StatementKey
	cleared [2]bool
	extrema [2]bool
}

// StatementFilter uses zero as a wildcard, matching the later SQL reset owner.
type StatementFilter struct {
	UserID, DatabaseID uint32
	QueryID            int64
}

func (f StatementFilter) matches(k StatementKey) bool {
	return (f.UserID == 0 || f.UserID == k.UserID) && (f.DatabaseID == 0 || f.DatabaseID == k.DatabaseID) && (f.QueryID == 0 || f.QueryID == k.QueryID)
}

type StatementInfo struct {
	Dealloc    int64
	StatsReset time.Time
}

// Statements is a bounded cluster-owned accumulator. Mu is a leaf: callers
// capture authority, settings, source and clocks before entering this owner.
// All returned values are copied. Clear never invokes callbacks or engine code.
type Statements struct {
	mu        sync.Mutex
	capacity  int
	entries   map[StatementKey]*statementEntry
	active    map[uint64]statementAdmission
	nextToken uint64
	median    float64
	info      StatementInfo
}

func NewStatements(capacity int, now time.Time) *Statements {
	if capacity < 1 {
		panic("statistics: invalid statement capacity")
	}
	return &Statements{capacity: capacity, entries: make(map[StatementKey]*statementEntry), active: make(map[uint64]statementAdmission), median: 10, info: StatementInfo{StatsReset: now}}
}

// Begin registers the admitted and known prospective terminal keys atomically.
// Normalized admission text and authority must be captured outside the leaf.
// For unchanged identity prospective equals admitted; no success is recorded.
func (s *Statements) Begin(admitted, prospective StatementKey, query string, now time.Time) StatementToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries[admitted] == nil {
		s.allocate(admitted, query, now, true)
	}
	s.nextToken++
	s.active[s.nextToken] = statementAdmission{keys: [2]StatementKey{admitted, prospective}}
	return StatementToken{id: s.nextToken}
}

// Release retires scalar admission ownership without deleting sticky history.
func (s *Statements) Release(token StatementToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, token.id)
}

// allocate requires mu. Only absent entries clone source or acquire timestamps.
func (s *Statements) allocate(key StatementKey, query string, now time.Time, sticky bool) *statementEntry {
	if len(s.entries) >= s.capacity {
		s.deallocate()
	}
	usage := float64(1)
	if sticky {
		usage = s.median
	}
	e := &statementEntry{StatementEntry: StatementEntry{Key: key, Query: strings.Clone(query), StatsSince: now, MinMaxStatsSince: now}, usage: usage}
	s.entries[key] = e
	return e
}

func (s *Statements) RecordPlan(token StatementToken, record StatementRecord) {
	s.record(token, record, true)
}
func (s *Statements) RecordExecution(token StatementToken, record StatementRecord) {
	s.record(token, record, false)
}
func (s *Statements) record(token StatementToken, record StatementRecord, planning bool) {
	if token.id == 0 || math.IsNaN(record.Milliseconds) || math.IsInf(record.Milliseconds, 0) || record.Milliseconds < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	admission, live := s.active[token.id]
	if !live || admission.cleared[0] {
		return
	}
	index := 0
	if record.Key != admission.keys[0] {
		index = 1
		if record.Key != admission.keys[1] {
			// An unanticipated utility identity begins resulting-key eligibility
			// atomically here. Earlier candidate-only clears are not its fences.
			admission.keys[1] = record.Key
			admission.cleared[1], admission.extrema[1] = false, false
			s.active[token.id] = admission
		}
	}
	if admission.cleared[index] {
		return
	}
	e := s.entries[record.Key]
	if e == nil {
		e = s.allocate(record.Key, record.Query, record.At, false)
	}
	if e.Planning.Count == 0 && e.Execution.Count == 0 {
		e.usage = 1
	}
	m := &e.Execution
	if planning {
		m = &e.Planning
	} else {
		e.Rows += record.Rows
	}
	e.Counters.Add(record.Counters)
	// A matching minmax clear fences only this key's new extrema baseline;
	// accepted in-flight samples still contribute cumulative moments and rows.
	oldMin, oldMax, oldN := m.Min, m.Max, m.extrema
	m.add(record.Milliseconds)
	if admission.extrema[index] {
		m.Min, m.Max, m.extrema = oldMin, oldMax, oldN
	}
	e.usage++
}
func (s *Statements) Snapshot() []StatementEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]StatementEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.Planning.Count == 0 && e.Execution.Count == 0 {
			continue
		}
		result = append(result, e.StatementEntry)
	}
	return result
}
func (s *Statements) Info() StatementInfo { s.mu.Lock(); defer s.mu.Unlock(); return s.info }

// Clear fences active admissions as well as clearing entries. Native database
// replacement prevents pre-replacement work from repopulating replaced history.
func (s *Statements) Clear(filter StatementFilter, minmaxOnly bool, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, admission := range s.active {
		for i, key := range admission.keys {
			if filter.matches(key) {
				if minmaxOnly {
					admission.extrema[i] = true
				} else {
					admission.cleared[i] = true
				}
			}
		}
		s.active[id] = admission
	}
	return s.resetEntriesLocked(filter, minmaxOnly, now)
}

// Reset is the SQL operation: eligible in-flight work may record its actual
// later successful phase. It neither creates nor changes active admissions,
// including any fences already imposed by native Clear.
func (s *Statements) Reset(filter StatementFilter, minmaxOnly bool, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetEntriesLocked(filter, minmaxOnly, now)
}

// resetEntriesLocked owns entry and global-info mutation under the leaf mutex.
func (s *Statements) resetEntriesLocked(filter StatementFilter, minmaxOnly bool, now time.Time) int {
	before := len(s.entries)
	count := 0
	for key, e := range s.entries {
		if !filter.matches(key) {
			continue
		}
		count++
		if minmaxOnly {
			e.Planning.clearExtrema()
			e.Execution.clearExtrema()
			e.MinMaxStatsSince = now
		} else {
			delete(s.entries, key)
		}
	}
	if (!minmaxOnly && count == before) || before == 0 {
		s.info = StatementInfo{StatsReset: now}
	}
	return count
}
func (s *Statements) deallocate() {
	victims := make([]*statementEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.Planning.Count == 0 && e.Execution.Count == 0 {
			e.usage *= 0.5
		} else {
			e.usage *= 0.99
		}
		victims = append(victims, e)
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].usage < victims[j].usage })
	s.median = victims[len(victims)/2].usage
	n := max(10, len(victims)*5/100)
	n = min(n, len(victims))
	for _, e := range victims[:n] {
		delete(s.entries, e.Key)
	}
	s.info.Dealloc++
}
