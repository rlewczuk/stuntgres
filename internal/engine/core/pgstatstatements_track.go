package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"time"
)

// statementStatsFrame belongs to one actual command/executor, not an adapter or
// prepared definition. Nested activation saves the parent's frame verbatim.
type statementStatsFrame struct {
	store                      *statistics.Statements
	token                      statistics.StatementToken
	key, terminalKey           statistics.StatementKey
	source                     string
	session                    *session.Session
	prospective                authorization.PrincipalRef
	pending                    parser.Statement
	utilityTarget              bool
	terminalCaptured           bool
	terminalEnabled            bool
	planning                   bool
	utility                    bool
	planStart, execStart       time.Time
	elapsed                    time.Duration
	rows                       int64
	executed, failed, finished bool
	reported                   bool
	pullRows                   bool
	finishStart                time.Time
	planElapsed                time.Duration
	planBuilt, planAccepted    bool
	admitted                   bool
	parent                     *statementStatsFrame
	planCounters, execCounters statistics.StatementCounters
}

// A synthesized PL cursor-argument query keeps declaration-ordered source,
// while its immutable syntax still uses each original operand's byte interval.
type statementStatsSpan struct {
	start, end, output int
}

func (s *StatementScope) statementStatsLocation(position int) int {
	if len(s.statementStatsSpans) != 0 {
		for _, span := range s.statementStatsSpans {
			if position >= span.start && position < span.end {
				return span.output + position - span.start
			}
		}
		return -1
	}
	return position - s.statementStatsOffset
}

func optimizableStatement(statement parser.Statement) bool {
	switch statement.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}
func statementStatsUtilityStatement(statement parser.Statement) bool {
	if optimizableStatement(statement) {
		return false
	}
	switch statement.(type) {
	case *parser.PrepareSQL, *parser.ExecuteSQL:
		return false
	}
	return true
}

func (s *StatementScope) statementStatsPolicy(utility bool) (enabled, planning bool) {
	return s.statementStatsPolicyAt(utility, s.statementStatsDepth == 0)
}

func (s *StatementScope) statementStatsPolicyAt(utility, topLevel bool) (enabled, planning bool) {
	if OperationReplayOrZero(s.Op).TableDestination || OperationReplayOrZero(s.Op).InsertAuthority {
		return false, false
	}
	store := s.Op.EffectiveSettings()
	store.Mu.Lock()
	defer store.Mu.Unlock()
	track := store.Value("pg_stat_statements.track")
	enabled = track == "all" || track == "top" && topLevel
	return enabled && (!utility || store.Value("pg_stat_statements.track_utility") == "on"), store.Value("pg_stat_statements.track_planning") == "on"
}

func statementStatsUtilityChild(statement parser.Statement) parser.Statement {
	switch statement := statement.(type) {
	case *parser.CreateQueryTable:
		return statement.Query
	case *parser.DeclareCursor:
		return statement.Query
	case *parser.Explain:
		return statement.Statement
	}
	return nil
}

func statementStatsUtilityRequiresBinding(statement parser.Statement) bool {
	if _, call := statement.(*parser.Call); call {
		return true
	}
	return statementStatsUtilityChild(statement) != nil
}
func (s *StatementScope) beginStatementStatsLocked(statement parser.Statement, b *analyze.Binding, userID uint32) {
	frame := s.statementStats
	if frame != nil && frame.admitted {
		return
	}
	utility := !optimizableStatement(statement)
	if utility && s.protocolOrZero().bind != nil {
		// Utility parameters may be prepared at Bind, but the command itself
		// has not run (notably CALL). Admit it only at actual Execute.
		return
	}
	child := statementStatsUtilityChild(statement)
	if child != nil && b != nil && frame == nil {
		if enabled, _ := s.statementStatsPolicy(true); !enabled {
			// Utility suppression never promotes its actual executor to a
			// top-level query. track=all may still admit that nested child.
			statement, utility = child, false
			s.statementStatsDepth++
			defer func() { s.statementStatsDepth-- }()
		}
	}
	if frame == nil {
		enabled, planning := s.statementStatsPolicy(utility)
		if !enabled {
			return
		}
		frame = &statementStatsFrame{store: s.Engine().Cluster.StatementStatistics, session: s.Op.Session, planning: planning, utility: utility,
			key: statistics.StatementKey{UserID: userID, DatabaseID: uint32(s.Engine().Catalog.DatabaseAddress().ObjectID), TopLevel: s.statementStatsDepth == 0}}
		frame.parent = s.statementStatsParent
		if utility {
			frame.execStart = time.Now()
		}
		s.statementStats = frame
	}
	if utility && statementStatsUtilityRequiresBinding(statement) && b == nil {
		// The utility hook precedes analysis. Retain its real start/policy,
		// but do not normalize or admit a raw stand-in for an analyzed child.
		return
	}
	if utility && !frame.utilityTarget {
		switch statement := statement.(type) {
		case *parser.RoleSelection, *parser.SessionAuthorization:
			frame.pending = statement
			return // The genuine role owner supplies its decided target once.
		case *parser.Discard:
			if statement.Category == parser.DiscardAll {
				frame.pending = statement
				return
			}
		}
	}
	var jumble analyze.StatementJumble
	anchor := 0
	if utility {
		if b != nil && statementStatsUtilityRequiresBinding(statement) {
			jumble, anchor = analyze.JumbleUtilityBound(statement, b)
		} else {
			jumble, anchor = analyze.JumbleUtility(statement)
		}
	} else {
		jumble = analyze.JumbleStatement(statement, b)
		switch q := statement.(type) {
		case *parser.Select:
			anchor = q.Pos.Offset
		case *parser.Insert:
			anchor = q.Pos.Offset
		case *parser.Update:
			anchor = q.Pos.Offset
		case *parser.Delete:
			anchor = q.Pos.Offset
		case *parser.Merge:
			anchor = q.Pos.Offset
		}
	}
	source := s.Op.OriginalSQL
	if s.statementStatsSource != "" {
		source = s.statementStatsSource
	} else if s.sqlRoutine.definition != nil {
		source = s.sqlRoutine.definition.Source
	}
	if s.statementStatsOffset != 0 || len(s.statementStatsSpans) != 0 {
		if len(s.statementStatsSpans) != 0 {
			anchor = 0
		} else {
			anchor -= s.statementStatsOffset
		}
		for i := range jumble.Locations {
			jumble.Locations[i] = s.statementStatsLocation(jumble.Locations[i])
		}
		for i := range jumble.Lists {
			jumble.Lists[i][0] = s.statementStatsLocation(jumble.Lists[i][0])
			jumble.Lists[i][1] = s.statementStatsLocation(jumble.Lists[i][1])
		}
		for i := range jumble.UtilityLocations {
			jumble.UtilityLocations[i] = s.statementStatsLocation(jumble.UtilityLocations[i])
		}
	}
	query, raw := parser.NormalizeStatementText(source, anchor, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: !s.Op.NonstandardStrings, Context: s.Op.Context()})
	now := time.Now()
	key := frame.key
	key.QueryID = jumble.QueryID()
	if s.statementStatsDepth == 0 && s.Op.CommandAdmission {
		s.Engine().activityProvider().ActivityQueryID(s.Op.Session.SessionToken, s.activityCommandIdentity(), key.QueryID)
	}
	prospective := key
	if frame.prospective != (authorization.PrincipalRef{}) {
		prospective.UserID = uint32(frame.prospective.ID())
	}
	frame.key, frame.source = key, raw
	frame.token = frame.store.Begin(key, prospective, query, now)
	frame.admitted = true
	frame.prospective = authorization.PrincipalRef{}
	frame.pending = nil
}
func (s *StatementScope) beginUtilityStatementStats(statement parser.Statement) error {
	if !statementStatsUtilityStatement(statement) {
		return nil
	}
	enabled, _ := s.statementStatsPolicy(true)
	if !enabled {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := s.statementAuthorizationLocked(statement)
	if err != nil {
		return err
	}
	s.beginStatementStatsLocked(statement, nil, uint32(auth.State.CurrentUser.ID()))
	return nil
}
func (s *StatementScope) statementStatsPlanStart() {
	f := s.statementStats
	if f != nil && !f.utility && f.planning && f.planStart.IsZero() && !f.planBuilt {
		f.planStart = time.Now()
	}
}

func (s *StatementScope) statementStatsPlanBuilt() {
	f := s.statementStats
	if f == nil || f.utility || f.planBuilt {
		return
	}
	if !f.planStart.IsZero() {
		f.planElapsed = time.Since(f.planStart)
		f.planStart = time.Time{}
	}
	f.planBuilt = true
}

func (s *StatementScope) statementStatsPlanAccepted() {
	f := s.statementStats
	if f == nil || f.utility || !f.planBuilt || f.planAccepted {
		return
	}
	f.planAccepted = true
	if f.planning {
		f.store.RecordPlan(f.token, statistics.StatementRecord{Key: f.key, Query: f.source, At: time.Now(), Milliseconds: float64(f.planElapsed) / float64(time.Millisecond), Counters: f.planCounters})
	}
	f.planCounters = statistics.StatementCounters{}
	f.execStart = time.Now()
}

// Planning-only portals have accepted immutable folds but no executor to end.
// A later Execute constructs its own executable program under current admission.
func (s *StatementScope) transferStatementStatsPlanningBind(bind *protocolPortal) {
	s.statementStatsPlanAccepted()
	bind.statementStats, s.statementStats = s.statementStats, nil
	if bind.statementStats != nil {
		bind.statementStats.parent = nil
	}
	if frame := bind.statementStats; frame != nil {
		frame.execStart = time.Time{}
	}
}
func (s *StatementScope) statementStatsReturned(result *Result, err error) {
	f := s.statementStats
	if f == nil {
		return
	}
	if f.reported {
		return
	}
	f.reported = true
	if err != nil {
		if f.pending != nil {
			e := s.Engine()
			e.Mu.Lock()
			f.utilityTarget = true
			s.beginStatementStatsLocked(f.pending, nil, f.key.UserID)
			e.Mu.Unlock()
		}
		f.failed = true
		return
	}
	if !f.admitted || !f.utility && !f.planAccepted {
		return
	}
	f.executed = true
	f.rows = result.Count
	if f.utility {
		s.captureStatementStatsTerminal(f)
		f.rows = result.UtilityRows
	}
	if !f.execStart.IsZero() {
		f.elapsed += time.Since(f.execStart)
		f.execStart = time.Time{}
	}
	if result.Rows != nil {
		if _, eager := result.Rows.(*exec.FinalRows); !eager {
			f.executed, f.pullRows = false, true
			f.rows = 0
			result.Rows = &statementStatsRows{RowResult: result.Rows, frame: f}
		}
	}
}
func (s *StatementScope) finishStatementStats(success bool) {
	for _, completed := range s.statementStatsCompletions {
		completed.finish(success)
	}
	s.statementStatsCompletions = nil
	if f := s.statementStats; f != nil && !f.finishStart.IsZero() {
		f.elapsed += time.Since(f.finishStart)
		f.finishStart = time.Time{}
	}
	s.captureStatementStatsTerminal(s.statementStats)
	s.statementStats.finish(success)
	s.statementStats = nil
}

func discardStatementStatsPortals(t *session.TransactionOwner) {
	payload := PayloadOf(t.Session)
	if payload != nil && payload.Cursors != nil {
		for _, cursor := range payload.Cursors.Entries {
			if cursor.tx == t.Ref && cursor.statementStats != nil {
				cursor.statementStats.failed = true
			}
		}
	}
	if payload == nil || payload.PreparedSQL == nil {
		return
	}
	for _, binding := range payload.PreparedSQL.Bindings() {
		if p := protocolPortalOf(binding); p != nil && p.Transaction == t.Ref && p.statementStats != nil {
			p.statementStats.failed = true
		}
	}
}
