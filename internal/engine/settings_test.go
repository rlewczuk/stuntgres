package engine

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestSettingUtilitySnapshotOwnership(t *testing.T) {
	all := [][]string{
		{"application_name", "initial", "Sets the application name to be reported in statistics and logs."},
		{"bytea_output", "hex", "Sets the output format for bytea."},
		{"client_encoding", "UTF8", "Sets the client's character set encoding."},
		{"client_min_messages", "notice", "Sets the message levels that are sent to the client."},
		{"DateStyle", "ISO, MDY", "Sets the display format for date and time values."},
		{"default_transaction_deferrable", "off", "Sets the default deferrable status of new transactions."},
		{"default_transaction_isolation", "read committed", "Sets the transaction isolation level of each new transaction."},
		{"default_transaction_read_only", "off", "Sets the default read-only status of new transactions."},
		{"extra_float_digits", "1", "Sets the number of digits displayed for floating-point values."},
		{"icu_validation_level", "warning", "Log level for reporting invalid ICU locale strings."},
		{"idle_in_transaction_session_timeout", "0", "Sets the maximum allowed idle time between queries, when in a transaction."},
		{"idle_session_timeout", "0", "Sets the maximum allowed idle time between queries, when not in a transaction."},
		{"IntervalStyle", "postgres", "Sets the display format for interval values."},
		{"lc_messages", "C", "Sets the language in which messages are displayed."},
		{"lc_monetary", "C", "Sets the locale for formatting monetary amounts."},
		{"lc_numeric", "C", "Sets the locale for formatting numbers."},
		{"lc_time", "C", "Sets the locale for formatting date and time values."},
		{"lock_timeout", "0", "Sets the maximum allowed duration of any wait for a lock."},
		{"log_statement_stats", "off", "Writes cumulative performance statistics to the server log."},
		{"search_path", `"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
		{"server_encoding", "UTF8", "Shows the server (database) character set encoding."},
		{"standard_conforming_strings", "on", "Causes '...' strings to treat backslashes literally."},
		{"statement_timeout", "0", "Sets the maximum allowed duration of any statement."},
		{"TimeZone", "UTC", "Sets the time zone for displaying and interpreting time stamps."},
		{"timezone_abbreviations", "Default", "Selects a file of time zone abbreviations."},
		{"transaction_deferrable", "off", "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
		{"transaction_isolation", "read committed", "Sets the current transaction's isolation level."},
		{"transaction_read_only", "off", "Sets the current transaction's read-only status."},
		{"transaction_timeout", "0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
	}
	for _, tc := range []struct {
		query    string
		names    []string
		rows     [][]string
		count    int64
		nullable bool
		null     bool
	}{
		{"SHOW application_name", []string{"application_name"}, [][]string{{"initial"}}, 0, false, false},
		{"SHOW ALL", []string{"name", "setting", "description"}, all, 0, false, false},
		{"SELECT current_setting('application_name')", []string{"current_setting"}, [][]string{{"initial"}}, 1, true, false},
		{"SELECT current_setting(NULL)", []string{"current_setting"}, [][]string{{""}}, 1, true, true},
		{"SELECT set_config('application_name','changed',false)", []string{"set_config"}, [][]string{{"changed"}}, 1, true, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "SET application_name TO initial")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, execErr := scope.Execute(statement(t, tc.query), 0, nil)
			finishErr := scope.Finish(execErr)
			if execErr != nil || finishErr != nil {
				t.Fatal(execErr, finishErr)
			}
			if !scope.completed || s.transactions.state != TxIdle {
				t.Fatal("utility did not complete and clear its implicit transaction")
			}
			if result.Rows == nil {
				t.Fatal("missing utility snapshot")
			}
			defer result.Rows.Close()
			actual := result.Rows.(*finalRows)
			if actual.self != actual || actual.count != len(tc.rows) || result.Count != tc.count || len(result.Rows.Schema()) != len(tc.names) {
				t.Fatalf("invalid result ownership, cardinality or metadata: %+v", result)
			}
			typ, _ := e.CatalogRead().LookupType(sqltype.Text)
			for i, column := range result.Rows.Schema() {
				if column.Name != tc.names[i] || column.Usage.Type != typ || column.Nullable != tc.nullable {
					t.Fatalf("column %d: %+v", i, column)
				}
			}
			// Completion releases the command, not the anonymous result owner.
			// Later cancellation, settings changes and teardown cannot erase it.
			cancel()
			op.Done()
			sessionSQL(t, s, "RESET ALL")
			s.RequestEnd(CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			values := make([]sqltype.Value, len(tc.names))
			for i, row := range tc.rows {
				if present, err := result.Rows.Next(context.Background(), values); !present || err != nil {
					t.Fatal(present, err)
				}
				for j, want := range row {
					if tc.null {
						if !values[j].IsNull() || values[j].Type() != typ {
							t.Fatal("lost typed NULL result")
						}
						continue
					}
					got, err := values[j].Text()
					if err != nil || got != want {
						t.Fatalf("row %d column %d: %q %v, want %q", i, j, got, err, want)
					}
				}
			}
			result.Rows.Close()
			if present, err := result.Rows.Next(context.Background(), values); present || err != nil || actual.anonymous != nil {
				t.Fatal("Close did not release anonymous rows")
			}
		})
	}
}

func TestSettingTypedInput(t *testing.T) {
	for _, tc := range []struct {
		d           SettingDefinition
		input, want string
	}{
		{SettingDefinition{Type: SettingBoolean}, "TRUE", "on"}, {SettingDefinition{Type: SettingBoolean}, "n", "off"},
		{SettingDefinition{Type: SettingInteger, IntegerMin: -10, IntegerMax: 10}, "2.5", "2"}, {SettingDefinition{Type: SettingInteger, IntegerMin: -10, IntegerMax: 10}, "3.5", "4"}, {SettingDefinition{Type: SettingInteger, IntegerMin: -10, IntegerMax: 10}, "010", "8"}, {SettingDefinition{Type: SettingInteger, IntegerMin: -10, IntegerMax: 10}, "0xA", "10"},
		{SettingDefinition{Type: SettingFloating, FloatMax: 10}, "2.1234567", "2.12346"},
		{SettingDefinition{Type: SettingEnum, EnumValues: []string{"safe", "fast"}}, "FAST", "fast"},
		{SettingDefinition{Type: SettingInteger, Unit: SettingKilobytes, IntegerMax: math.MaxInt32}, "30.1GB", "30822MB"},
		{SettingDefinition{Type: SettingInteger, Unit: SettingKilobytes, IntegerMax: math.MaxInt32}, "0.5kB", "0"},
		{SettingDefinition{Type: SettingInteger, Unit: SettingBytes, IntegerMax: math.MaxInt32}, "0.5kB", "512B"},
		{SettingDefinition{Type: SettingInteger, Unit: SettingMilliseconds, IntegerMax: math.MaxInt32}, "1.5s", "1500ms"},
		{SettingDefinition{Type: SettingFloating, Unit: SettingSeconds, FloatMax: 10}, "0.001s", "1ms"},
		{SettingDefinition{Type: SettingFloating, Unit: SettingSeconds, FloatMax: 10}, "0.0000001", "0.1us"},
		{SettingDefinition{Type: SettingFloating, Unit: SettingSeconds, FloatMax: 10}, "0.0000001s", "0"},
		{SettingDefinition{Type: SettingFloating, FloatMax: 10}, "0x1.8p0", "1.5"},
		{SettingDefinition{Type: SettingInteger, IntegerMin: -10, IntegerMax: 10}, "-0.5", "0"},
	} {
		tc.d.Name = "demo.value"
		v, err := tc.d.parse(tc.input)
		if err != nil || tc.d.format(v) != tc.want {
			t.Errorf("%+v %q got %q err %v want %q", tc.d, tc.input, tc.d.format(v), err, tc.want)
		}
	}
	for _, input := range []string{"0b10", "0o10", "1_000", "08", "NaN", "1kB", "1 2", "1e999"} {
		d := SettingDefinition{Name: "demo.count", Type: SettingInteger, IntegerMax: 10}
		if _, err := d.parse(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	for _, input := range []string{"o", " true ", ""} {
		if _, err := (SettingDefinition{Name: "demo.flag", Type: SettingBoolean}).parse(input); err == nil {
			t.Errorf("accepted Boolean %q", input)
		}
	}
	for _, input := range []string{"NaN", "1e999", "1e-999", "1e-320", "1_000", "0b10"} {
		if _, err := (SettingDefinition{Name: "demo.real", Type: SettingFloating, FloatMax: 10}).parse(input); err == nil {
			t.Errorf("accepted real %q", input)
		}
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
	want, _, _ := normalizeApplicationName(p.value)
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

func (p *settingsDefaultProviderForTest) SessionDefault(_ context.Context, d SettingDefinition) (string, bool, error) {
	p.calls[d.Name]++
	if len(d.EnumValues) > 0 {
		d.EnumValues[0] = "provider-mutated-copy"
	}
	value, present := p.values[d.Name]
	return value, present, nil
}
func TestSettingProviderDefaultsAndDescriptorOwnership(t *testing.T) {
	defs := []SettingDefinition{{Name: "demo.mode", Type: SettingEnum, BootDefault: "FAST", EnumValues: []string{"safe", "fast"}}, {Name: "demo.ratio", Type: SettingFloating, BootDefault: "1.23456789", FloatMax: 10}}
	for i := range defs {
		var err error
		defs[i], err = ValidateSetting(defs[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if defs[0].BootDefault != "fast" || defs[1].BootDefault != "1.23457" {
		t.Fatal(defs)
	}
	e := NewWithSettings(defs, "")
	p := &settingsDefaultProviderForTest{values: map[string]string{"demo.mode": "FAST"}, calls: map[string]int{}}
	a := sessionStart(t, e, p)
	for _, name := range e.settingsOrder {
		if p.calls[name] != 1 {
			t.Fatal(name, p.calls)
		}
	}
	if a.settings.defaultTyped["demo.ratio"].number != 1.23456789 {
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
		if s, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(testSessionID.Add(1)), Defaults: bad}); err == nil || s != nil {
			t.Fatal("intrinsic default changed", s, err)
		}
	}
}

func TestSettingExactDecimalDefaultCapture(t *testing.T) {
	exact := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil).String() + "e-1023"
	def, err := ValidateSetting(SettingDefinition{Name: "demo.ratio", Type: SettingFloating, BootDefault: exact, FloatMin: -10, FloatMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	if def.bootValue.number != 0x1p-1023 {
		t.Fatal("registered default lost exactness", def.bootValue)
	}
	e := NewWithSettings([]SettingDefinition{def}, "")
	p := &settingsDefaultProviderForTest{values: map[string]string{"demo.ratio": "-" + exact}, calls: map[string]int{}}
	a := sessionStart(t, e, p)
	if p.calls["demo.ratio"] != 1 || a.settings.defaultTyped["demo.ratio"].number != -0x1p-1023 {
		t.Fatal("provider default lost exactness", p.calls, a.settings.defaultTyped)
	}
	p.values["demo.ratio"] = "1"
	sessionSQL(t, a, "SET demo.ratio TO '2'")
	sessionSQL(t, a, "RESET demo.ratio")
	if got := tuples(sessionSQL(t, a, "SHOW demo.ratio"))[0][0]; got != "-1.11254e-308" {
		t.Fatal(got)
	}
	if a.settings.typedValue("demo.ratio").number != -0x1p-1023 {
		t.Fatal("reset reparsed rounded display")
	}
	b := sessionStart(t, e, p)
	if b.settings.defaultTyped["demo.ratio"].number != 1 {
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
