package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

type transactionTerminalObserver struct {
	end func(ids.SessionToken, session.EndReason) error
}

func (o *transactionTerminalObserver) EndSession(token ids.SessionToken, reason session.EndReason) error {
	return o.end(token, reason)
}

func transactionMechanicalConnection(t *testing.T) *conn {
	t.Helper()
	connector, err := NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	t.Cleanup(func() { c.Close() })
	op, err := c.session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if _, err := c.engine.Begin(op, session.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTransactionTerminalReasons(t *testing.T) {
	for _, reason := range []session.EndReason{session.CleanClose, session.Terminated, session.Abandoned} {
		t.Run(map[session.EndReason]string{session.CleanClose: "close", session.Terminated: "terminate", session.Abandoned: "abandon"}[reason], func(t *testing.T) {
			c := transactionMechanicalConnection(t)
			s := c.session
			token := s.Token()
			var order []session.ResourceKind
			failure := errors.New("terminal observer")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			// PreparedSQL is now a production-owned attachment. The adapter's
			// terminal-error witness uses the still-unoccupied Temporary slot;
			// complete resource ordering is independently checked by engine units.
			_, err = op.Attach(session.Temporary, &transactionTerminalObserver{end: func(got ids.SessionToken, r session.EndReason) error {
				if got != token || r != reason {
					t.Error("terminal identity/reason", got, r, token, reason)
				}
				if s.TransactionState() != session.TxIdle {
					t.Error("transaction cleanup must precede resource cleanup")
				}
				order = append(order, session.Temporary)
				return failure
			}})
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			op.Done()
			switch reason {
			case session.CleanClose:
				if err := c.Close(); !errors.Is(err, failure) {
					t.Fatal("Close cleanup error", err)
				}
			case session.Terminated:
				s.RequestEnd(session.Terminated)
			case session.Abandoned:
				cleanupSession(cleanupCapsule{s})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.WaitEnd(ctx); !errors.Is(err, failure) {
				t.Fatal("WaitEnd cleanup error", err)
			}
			if s.IsLive() || s.TransactionState() != session.TxIdle || c.IsValid() {
				t.Fatal("terminal session remained live/active/valid")
			}
			want := []session.ResourceKind{session.Temporary}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("resource drain order", order, want)
			}
			s.RequestEnd(session.CleanClose)
			s.RequestEnd(session.Terminated)
			s.RequestEnd(session.Abandoned)
			if err := s.WaitEnd(ctx); !errors.Is(err, failure) {
				t.Fatal("repeated end lost cleanup error", err)
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("repeated end repeated cleanup", order)
			}
		})
	}
}

func TestTransactionRawReuseState(t *testing.T) {
	c := transactionMechanicalConnection(t)
	token := c.session.Token()
	if err := c.ResetSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.session.TransactionState() != session.TxActive {
		t.Fatal("ResetSession ended active transaction")
	}
	_, err := normalizeArguments([]driver.NamedValue{{Name: "v", Ordinal: 1, Value: int64(2)}})
	if err == nil || err.Error() != "stuntgres: named parameters are not supported" {
		t.Fatal(err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("adapter rejection fabricated SQLSTATE", err)
	}
	if c.session.TransactionState() != session.TxActive || c.session.Token() != token || !c.IsValid() {
		t.Fatal("adapter rejection changed active physical owner")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.session.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	if c.session.IsLive() || c.session.TransactionState() != session.TxIdle {
		t.Fatal("physical close did not end transaction")
	}
}
