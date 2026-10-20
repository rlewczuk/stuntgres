package engine

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SQL values/routes are covered by TestReferenceSettingsUtilitySnapshotOwnership.
// This unit retains only package-private completion and detached-row ownership.
func TestSettingUtilitySnapshotOwnership(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "initial"}, calls: map[string]int{}}
	sess := sessionStart(t, e, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := sess.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, execErr := scope.Execute(&parser.Show{Name: parser.Identifier{Text: "application_name"}}, 0, nil)
	if err := scope.Finish(execErr); err != nil {
		t.Fatal(err)
	}
	if !scope.completed || sess.Transactions.State != TxIdle {
		t.Fatal("utility retained implicit transaction ownership")
	}
	actual, ok := result.Rows.(*exec.FinalRows)
	if !ok || actual.Self != actual || actual.Count != 1 {
		t.Fatal("invalid anonymous snapshot owner")
	}
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	if actual.Schema()[0].Usage.Type != typ {
		t.Fatal("foreign snapshot type")
	}
	cancel()
	op.Done()
	sess.Settings.Mu.Lock()
	sess.Settings.Values["application_name"] = "changed"
	sess.Settings.Mu.Unlock()
	sess.RequestEnd(CleanClose)
	if err := sess.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	values := make([]sqltype.Value, 1)
	if present, err := actual.Next(context.Background(), values); !present || err != nil {
		t.Fatal(present, err)
	}
	if text, err := values[0].Text(); err != nil || text != "initial" {
		t.Fatal("detached snapshot consulted ended owner", text, err)
	}
	actual.Close()
	if present, err := actual.Next(context.Background(), values); present || err != nil || actual.Anonymous != nil {
		t.Fatal("Close retained anonymous row ownership")
	}
	// A whole typed NULL likewise belongs to the anonymous result, not a session.
	nullResult, err := anonymousResult([]catalog.Column{{Name: "v", Usage: typ.Usage(), Nullable: true}}, [][]sqltype.Value{{sqltype.Null(typ)}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer nullResult.Rows.Close()
	if present, err := nullResult.Rows.Next(context.Background(), values); !present || err != nil || !values[0].IsNull() || values[0].Type() != typ {
		t.Fatal("lost typed NULL ownership", err)
	}
}

func TestSettingAdmissionTimerAtomicity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE sentinel(v INT)")
	sessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
	retained := sessionSQL(t, s, "SELECT v FROM sentinel")
	sessionSQL(t, s, "SET statement_timeout TO '1ms'")
	op, options, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if !options.StandardConformingStrings {
		t.Fatal("wrong mode")
	}
	ast := statement(t, "INSERT INTO sentinel VALUES(8),(9)")
	e.mu.Lock()
	finished := make(chan error, 1)
	go func() { _, err := e.Execute(op, ast, 0, nil); finished <- err }()
	select {
	case <-op.Context().Done():
	case <-time.After(time.Second):
		e.mu.Unlock()
		t.Fatal("timer did not fire")
	}
	e.mu.Unlock()
	err = op.CommandError(<-finished)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "57014" || d.Message != "canceling statement due to statement timeout" {
		t.Fatal(err)
	}
	op.Done()
	sessionSQL(t, s, "RESET statement_timeout")
	if got := tuples(sessionSQL(t, s, "SELECT v FROM sentinel")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal(got)
	}
	if got := tuples(retained); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal(got)
	}
	if got := tuples(sessionSQL(t, peer, "SELECT v FROM sentinel")); len(got) != 1 || got[0][0] != int64(7) {
		t.Fatal(got)
	}
	caller, cancel := context.WithCancel(context.Background())
	op, _, err = s.AcquireCommand(caller)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if !errors.Is(op.CommandError(op.Context().Err()), context.Canceled) {
		t.Fatal("caller cancellation translated")
	}
	op.Done()
}
func TestSettingCapturedDefaults(t *testing.T) {
	e := New()
	p := &defaultProvider{value: "éééééééééééééééééééé"}
	s := sessionStart(t, e, p)
	d, err := e.settings.Lookup("application_name")
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := d.Parse(p.value)
	if err != nil {
		t.Fatal(err)
	}
	want := normalized.Text
	sessionSQL(t, s, "SET application_name TO changed")
	sessionSQL(t, s, "RESET application_name")
	sessionValue(t, s, want)
	sessionSQL(t, s, "SET application_name TO changed")
	sessionSQL(t, s, "RESET ALL")
	sessionValue(t, s, want)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := e.Execute(op, &parser.SettingCall{Name: "set_config", Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "application_name"}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue}}}}, 0, nil)
	if err != nil || tuples(r)[0][0] != want {
		t.Fatal(r, err)
	}
}

type settingsDefaultProviderForTest struct {
	values map[string]string
	calls  map[string]int
}

func (p *settingsDefaultProviderForTest) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	p.calls[d.Name]++
	if len(d.EnumValues) > 0 {
		d.EnumValues[0] = "provider-mutated-copy"
	}
	value, present := p.values[d.Name]
	return value, present, nil
}
func TestSettingProviderDefaultsAndDescriptorOwnership(t *testing.T) {
	defs := []settings.Definition{{Name: "demo.mode", Type: settings.Enum, BootDefault: "FAST", EnumValues: []string{"safe", "fast"}}, {Name: "demo.ratio", Type: settings.Floating, BootDefault: "1.23456789", FloatMax: 10}}
	for i := range defs {
		var err error
		defs[i], err = settings.Validate(defs[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if defs[0].BootDefault != "fast" || defs[1].BootDefault != "1.23457" {
		t.Fatal(defs)
	}
	e := NewWithSettings(defs, "", false)
	p := &settingsDefaultProviderForTest{values: map[string]string{"demo.mode": "FAST"}, calls: map[string]int{}}
	a := sessionStart(t, e, p)
	for _, name := range e.settings.Order() {
		if p.calls[name] != 1 {
			t.Fatal(name, p.calls)
		}
	}
	if a.Settings.DefaultTyped["demo.ratio"].Number != 1.23456789 {
		t.Fatal("default lost precision")
	}
	if got := tuples(sessionSQL(t, a, "SHOW demo.mode"))[0][0]; got != "fast" {
		t.Fatal(got)
	}
	sessionSQL(t, a, "SET demo.mode TO safe")
	p.values["demo.mode"] = "safe"
	sessionSQL(t, a, "RESET demo.mode")
	if got := tuples(sessionSQL(t, a, "SHOW demo.mode"))[0][0]; got != "fast" {
		t.Fatal(got)
	}
	b := sessionStart(t, e, p)
	if got := tuples(sessionSQL(t, b, "SHOW demo.mode"))[0][0]; got != "safe" {
		t.Fatal(got)
	}
	sessionSQL(t, b, "SET demo.mode TO FAST") // Provider could not corrupt shared enum definitions.
	for _, name := range []string{"server_encoding", "log_statement_stats"} {
		value := "LATIN1"
		if name == "log_statement_stats" {
			value = "on"
		}
		bad := &settingsDefaultProviderForTest{values: map[string]string{name: value}, calls: map[string]int{}}
		if s, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: bad}); err == nil || s != nil {
			t.Fatal("intrinsic default changed", s, err)
		}
	}
}

func TestSettingExactDecimalDefaultCapture(t *testing.T) {
	exact := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil).String() + "e-1023"
	def, err := settings.Validate(settings.Definition{Name: "demo.ratio", Type: settings.Floating, BootDefault: exact, FloatMin: -10, FloatMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	if def.BootValue().Number != 0x1p-1023 {
		t.Fatal("registered default lost exactness", def.BootValue())
	}
	e := NewWithSettings([]settings.Definition{def}, "", false)
	p := &settingsDefaultProviderForTest{values: map[string]string{"demo.ratio": "-" + exact}, calls: map[string]int{}}
	a := sessionStart(t, e, p)
	if p.calls["demo.ratio"] != 1 || a.Settings.DefaultTyped["demo.ratio"].Number != -0x1p-1023 {
		t.Fatal("provider default lost exactness", p.calls, a.Settings.DefaultTyped)
	}
	p.values["demo.ratio"] = "1"
	sessionSQL(t, a, "SET demo.ratio TO '2'")
	sessionSQL(t, a, "RESET demo.ratio")
	if got := tuples(sessionSQL(t, a, "SHOW demo.ratio"))[0][0]; got != "-1.11254e-308" {
		t.Fatal(got)
	}
	if a.Settings.TypedValue("demo.ratio").Number != -0x1p-1023 {
		t.Fatal("reset reparsed rounded display")
	}
	b := sessionStart(t, e, p)
	if b.Settings.DefaultTyped["demo.ratio"].Number != 1 {
		t.Fatal("replacement did not capture new default")
	}
}

func TestSettingAdmissionCapturesNoticeFilter(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	// A concurrent native command may change current state after admission;
	// the already admitted command retains its original threshold.
	sessionSQL(t, s, "SET client_min_messages TO error")
	input := strings.Repeat("a", 64)
	if _, err = e.Execute(op, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{input}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	got := op.DetachNotices()
	want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + input + `" will be truncated to "` + strings.Repeat("a", 63) + `"`}
	if len(got) != 1 || got[0] != want {
		t.Fatal(got)
	}
	op.Done()
	sessionSQL(t, s, "SET statement_timeout TO '1ms'")
	op, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	select {
	case <-op.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("actual admission timer did not fire")
	}
	_, err = e.Execute(op, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{strings.Repeat("b", 64)}}, 0, nil)
	d, ok := op.CommandError(err).(*sqlstate.Error)
	if !ok || d.Code != "57014" || op.DetachNotices() != nil {
		t.Fatal(err, d)
	}
	op.Done()
	sessionSQL(t, s, "RESET statement_timeout")
	sessionValue(t, s, strings.Repeat("a", 63))
}

func TestSettingDefaultsRemainSilentAtNoticeThreshold(t *testing.T) {
	e := New()
	p := &settingsDefaultProviderForTest{values: map[string]string{"application_name": strings.Repeat("a", 64), "client_min_messages": "notice"}, calls: map[string]int{}}
	a := sessionStart(t, e, p)
	for _, q := range []string{"SET application_name TO DEFAULT", "RESET application_name", "RESET ALL", "SELECT set_config('application_name',NULL,false)"} {
		op, _, err := a.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, q), 0, nil)
		notices := op.DetachNotices()
		op.Done()
		if err != nil || notices != nil {
			t.Fatal(q, err, notices)
		}
		sessionValue(t, a, strings.Repeat("a", 63))
	}
	p.values["application_name"] = "new"
	b := sessionStart(t, e, p)
	sessionValue(t, b, "new")
	sessionValue(t, a, strings.Repeat("a", 63))
}
