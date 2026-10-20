package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func localOwnerValue(t *testing.T, s *Session, name, want string) {
	t.Helper()
	s.settings.mu.Lock()
	defer s.settings.mu.Unlock()
	if got := s.settings.value(name); got != want {
		t.Fatalf("%s got=%q want=%q", name, got, want)
	}
	if len(s.settings.actions) != 0 {
		t.Fatal("retained transactional actions", s.settings.actions)
	}
}
func TestSettingsLocalLifecycle(t *testing.T) {
	for _, mode := range []string{"abort", "cancel_commit", "replace_command", "replace_commit", "foreign_ref", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "SET application_name='b'")
			ref := transactionBegin(t, s)
			sessionSQL(t, s, "SET application_name='s'")
			sessionSQL(t, s, "SET LOCAL application_name='l'")
			switch mode {
			case "abort", "replace_command":
				if mode == "replace_command" {
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				op := transactionOperation(t, s)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				_, err = scope.Execute(statement(t, "SET absent_setting='x'"), 0, nil)
				if err == nil {
					t.Fatal("expected error")
				}
				scope.Finish(err)
				op.Done()
				localOwnerValue(t, s, "application_name", "b")
				if s.TransactionState() != TxAborted {
					t.Fatal("not aborted")
				}
				transactionEnd(t, s, ref, false)
			case "cancel_commit":
				ctx, cancel := context.WithCancel(context.Background())
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				_, err = e.Commit(op, ref)
				op.Done()
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "replace_commit":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				op := transactionOperation(t, s)
				_, err := e.Commit(op, ref)
				op.Done()
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "foreign_ref":
				op := transactionOperation(t, s)
				_, err := e.Commit(op, ids.TxRef{})
				op.Done()
				if err == nil {
					t.Fatal("foreign accepted")
				}
				s.settings.mu.Lock()
				got := s.settings.value("application_name")
				s.settings.mu.Unlock()
				if got != "l" {
					t.Fatal("foreign ended live tx", got)
				}
				transactionEnd(t, s, ref, false)
			case "cleanup":
				if err := endTransactionSession(s.transactions, s.Token()); err != nil {
					t.Fatal(err)
				}
			}
			localOwnerValue(t, s, "application_name", "b")
		})
	}
}

func TestSettingsLocalTypedConsumersAbort(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ref := transactionBegin(t, s)
	sessionSQL(t, s, "SET LOCAL standard_conforming_strings=off")
	sessionSQL(t, s, "SET LOCAL statement_timeout='1s'")
	sessionSQL(t, s, "SET LOCAL client_min_messages=error")
	op := transactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(statement(t, "SET absent_setting='x'"), 0, nil)
	scope.Finish(err)
	op.Done()
	localOwnerValue(t, s, "standard_conforming_strings", "on")
	localOwnerValue(t, s, "statement_timeout", "0")
	localOwnerValue(t, s, "client_min_messages", "notice")
	op, options, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if !options.StandardConformingStrings {
		t.Fatal("next admission captured stale parser mode")
	}
	if _, ok := op.Ctx.Deadline(); ok {
		t.Fatal("next admission captured stale timeout")
	}
	if op.NoticeThreshold != levelNotice {
		t.Fatal("next admission captured stale notice threshold")
	}
	transactionEnd(t, s, ref, false)
}

func TestSettingsLocalCapturedDefaults(t *testing.T) {
	e := New()
	p := &settingsDefaultProviderForTest{values: map[string]string{"application_name": "startup"}, calls: map[string]int{}}
	s := sessionStart(t, e, p)
	for _, value := range []string{"DEFAULT", "'startup'", "''"} {
		ref := transactionBegin(t, s)
		sessionSQL(t, s, "SET application_name='s'")
		sessionSQL(t, s, "SET LOCAL application_name="+value)
		sessionSQL(t, s, "RESET ALL")
		transactionEnd(t, s, ref, true)
		want := "startup"
		if value == "DEFAULT" {
			want = "s"
		}
		localOwnerValue(t, s, "application_name", want)
	}
	for name, calls := range p.calls {
		if calls != 1 {
			t.Fatal(name, calls)
		}
	}
}
