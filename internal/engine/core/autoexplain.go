package core

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// One actual executor owns this frame. Nothing here enters a prepared recipe,
// database snapshot or physical settings store. Retained producers transfer it.
type autoExplainFrame struct {
	options                              explain.Options
	minimum                              time.Duration
	parameterLimit                       int
	level                                session.NoticeLevel
	severity                             string
	buffers, wal, triggers               bool
	source, parameters                   string
	context                              string // Detached actual live routine/SPI traceback at activation.
	renderers                            []*explain.Renderer
	observation                          *exec.Observation
	started                              time.Time
	elapsed                              time.Duration
	executed, failed, returned, finished bool
	parameterCaptured                    bool
	primary                              parser.Statement
}

var autoExplainDecisions = [2]bool{false, true} // Borrowed immutable family decisions.

func autoExplainSample(rate float64, draw func() float64) bool {
	return rate >= 1 || rate > 0 && draw() < rate
}
func autoExplainThreshold(elapsed, minimum time.Duration) bool {
	return minimum >= 0 && elapsed >= minimum
}

func (f *autoExplainFrame) resume() {
	if f != nil && !f.finished && f.started.IsZero() {
		f.started = time.Now()
	}
}
func (f *autoExplainFrame) pause() {
	if f != nil && !f.started.IsZero() {
		f.elapsed += time.Since(f.started)
		f.started = time.Time{}
	}
}

// Called under Engine.Mu only after current binding/import/preflight. Sampling
// is family-local and inherits even a sampled-out parent's decision.
func (s *StatementScope) beginAutoExplainLocked(statement parser.Statement) (*autoExplainFrame, error) {
	if s.autoExplain != nil {
		return s.autoExplain, nil
	}
	if exec.InParallelOperation(s.executionContext()) {
		return nil, nil
	}
	if _, call := statement.(*parser.Call); call {
		return nil, nil
	}
	if s.protocolOrZero().bind != nil {
		return nil, nil
	}
	store := s.Op.EffectiveSettings()
	if !store.AutoExplainLoaded() {
		return nil, nil
	}
	store.Mu.Lock()
	defer store.Mu.Unlock()
	if !store.AutoExplainLoaded() {
		return nil, nil
	}
	// Resolving one definition reconciles a detached pre-load image first.
	_, _ = store.EffectiveDefinitionLocked(s.Engine().Settings, "auto_explain.log_min_duration")
	minimum := store.TypedValue("auto_explain.log_min_duration").Number
	if s.autoExplainFamily == nil {
		selected := minimum >= 0 && autoExplainSample(store.TypedValue("auto_explain.sample_rate").Number, rand.Float64)
		s.autoExplainFamily = &autoExplainDecisions[0]
		if selected {
			s.autoExplainFamily = &autoExplainDecisions[1]
		}
	}
	if minimum < 0 || !*s.autoExplainFamily || s.statementStatsDepth != 0 && !store.TypedValue("auto_explain.log_nested_statements").Boolean {
		return nil, nil
	}
	f := &autoExplainFrame{minimum: time.Duration(minimum) * time.Millisecond, parameterLimit: int(store.TypedValue("auto_explain.log_parameter_max_length").Number)}
	f.options = explain.Options{Costs: true, Analyze: store.TypedValue("auto_explain.log_analyze").Boolean, Verbose: store.TypedValue("auto_explain.log_verbose").Boolean, Timing: store.TypedValue("auto_explain.log_timing").Boolean, Format: store.TypedValue("auto_explain.log_format").Text}
	f.options.Timing = f.options.Timing && f.options.Analyze
	f.buffers = f.options.Analyze && store.TypedValue("auto_explain.log_buffers").Boolean
	f.wal = f.options.Analyze && store.TypedValue("auto_explain.log_wal").Boolean
	f.triggers = f.options.Analyze && store.TypedValue("auto_explain.log_triggers").Boolean
	level := store.TypedValue("auto_explain.log_level").Text
	rank, _ := settings.MessageLevelRank(level)
	f.level, f.severity = session.NoticeLevel(rank), strings.ToUpper(level)
	if strings.HasPrefix(level, "debug") {
		f.severity = "DEBUG"
	}
	f.source = s.Op.OriginalSQL
	if s.statementStatsSource != "" {
		f.source = s.statementStatsSource
	} else if s.sqlRoutine.definition != nil {
		f.source = s.sqlRoutine.definition.Source
	}
	f.context = s.currentPLContext()
	s.autoExplain = f
	f.primary = s.autoExplainStatement
	if f.primary == nil {
		f.primary = statement
	}
	return f, nil
}

// Text output can consult current presentation readers. Never perform it while
// holding Engine/settings/Session locks, and re-fence before executor startup.
func (e *Engine) captureAutoExplainParametersLocked(s *StatementScope, f *autoExplainFrame, values []sqltype.Value) error {
	if f == nil || f.parameterCaptured {
		return nil
	}
	f.parameterCaptured = true
	if f.parameterLimit == 0 || len(values) == 0 {
		return nil
	}
	e.Mu.Unlock()
	text, err := autoExplainParameters(s.executionContext(), values, f.parameterLimit, s.Output)
	e.Mu.Lock()
	if err != nil {
		return err
	}
	if err := e.validateOperation(s.Op); err != nil {
		return err
	}
	if err := s.ValidateLockedRelation(e); err != nil {
		return err
	}
	f.parameters = text
	return nil
}

func autoExplainParameters(ctx context.Context, values []sqltype.Value, limit int, output sqltype.OutputEnvironment) (string, error) {
	if limit == 0 || len(values) == 0 {
		return "", nil
	}
	var out strings.Builder
	for i, value := range values {
		if i != 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(&out, "$%d = ", i+1)
		if value.IsNull() {
			out.WriteString("NULL")
			continue
		}
		text, err := value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return "", err
		}
		clipped := limit > 0 && len(text) > limit
		if clipped {
			end := limit
			for end > 0 && !utf8.RuneStart(text[end]) {
				end--
			}
			text = text[:end]
		}
		out.WriteByte('\'')
		out.WriteString(strings.ReplaceAll(text, "'", "''"))
		if clipped {
			out.WriteString("...")
		}
		out.WriteByte('\'')
	}
	return out.String(), nil
}

func (s *StatementScope) autoExplainReturned(result *Result, err error) {
	f := s.autoExplain
	if f == nil || f.returned {
		return
	}
	f.returned = true
	f.pause()
	if err != nil {
		f.failed = true
		return
	}
	f.executed = true
	if result.Rows != nil {
		if _, eager := result.Rows.(*exec.FinalRows); !eager {
			f.executed = false
			result.Rows = &autoExplainRows{RowResult: result.Rows, frame: f}
		}
	}
}

func (f *autoExplainFrame) finish(op *session.Operation, success bool) {
	if f == nil || f.finished {
		return
	}
	f.pause()
	f.finished = true
	defer func() {
		f.renderers, f.observation = nil, nil
		f.source, f.parameters, f.context = "", "", ""
		f.primary = nil
	}()
	if !success || f.failed || !f.executed || !autoExplainThreshold(f.elapsed, f.minimum) || op == nil {
		return
	}
	message, err := explain.LogOutput(f.options, f.renderers, f.source, f.parameters, f.elapsed, f.observation, f.buffers, f.wal, f.triggers)
	// Presentation must never replace an ordinary statement outcome.
	if err != nil {
		return
	}
	code := "00000"
	if f.level == session.LevelWarning {
		code = "01000"
	}
	op.EmitNotice(f.level, sqlstate.Error{Severity: f.severity, SeverityUnlocalized: f.severity, Code: code, Message: message, Context: f.context})
}
func (s *StatementScope) finishAutoExplain(success bool) {
	for _, f := range s.autoExplainCompletions {
		s.finishAutoExplainFrame(f, success)
	}
	s.autoExplainCompletions = nil
	s.finishAutoExplainFrame(s.autoExplain, success)
	s.autoExplain, s.autoExplainFamily = nil, nil
}

// Automatic diagnostics already own their complete live traceback. Mark the
// exact emitted record, not its text prefix, so later lexical notice boundaries
// do not append the same frames again. Index reuse after rollback/detachment is
// checked against that owned record.
func (s *StatementScope) finishAutoExplainFrame(f *autoExplainFrame, success bool) {
	if f == nil {
		return
	}
	start := len(s.Op.Notices)
	f.finish(s.Op, success)
	if start < len(s.Op.Notices) && s.Op.Notices[start].Context != "" {
		if s.autoExplainNoticeContexts == nil {
			s.autoExplainNoticeContexts = make(map[int]sqlstate.Error)
		}
		s.autoExplainNoticeContexts[start] = s.Op.Notices[start]
	}
}

type autoExplainRows struct {
	exec.RowResult
	frame  *autoExplainFrame
	closed bool
}

func (r *autoExplainRows) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	r.frame.resume()
	present, err := r.RowResult.Next(r.frame.observation.Context(ctx), row)
	r.frame.pause()
	if err != nil {
		r.frame.failed = true
	}
	return present, err
}
func (r *autoExplainRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.frame.resume()
	err := r.RowResult.Close()
	r.frame.pause()
	r.frame.failed = r.frame.failed || err != nil
	r.frame.executed = err == nil
	return err
}

func (s *StatementScope) autoExplainObservation() *exec.Observation {
	if s.autoExplain == nil {
		return nil
	}
	return s.autoExplain.observation
}
func (s *StatementScope) recordAutoExplainWrite(count int64) {
	s.autoExplainObservation().RecordWrite(count)
}
