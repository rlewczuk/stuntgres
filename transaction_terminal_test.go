package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
)

type transactionTerminalObserver struct {
	end func(engine.SessionToken, engine.EndReason) error
}

func (o *transactionTerminalObserver) EndSession(token engine.SessionToken, reason engine.EndReason) error {
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
	if _, err := c.session.Engine().Begin(op, engine.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTransactionTerminalReasons(t *testing.T) {
	for _, reason := range []engine.EndReason{engine.CleanClose, engine.Terminated, engine.Abandoned} {
		t.Run(map[engine.EndReason]string{engine.CleanClose: "close", engine.Terminated: "terminate", engine.Abandoned: "abandon"}[reason], func(t *testing.T) {
			c := transactionMechanicalConnection(t)
			s := c.session
			token := s.Token()
			var order []engine.ResourceKind
			failure := errors.New("terminal observer")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []engine.ResourceKind{engine.Temporary, engine.PreparedSQL} {
				k := kind
				_, err := op.Attach(k, &transactionTerminalObserver{end: func(got engine.SessionToken, r engine.EndReason) error {
					if got != token || r != reason {
						t.Error("terminal identity/reason", got, r, token, reason)
					}
					if s.TransactionState() != engine.TxIdle {
						t.Error("transaction cleanup must precede resource cleanup")
					}
					order = append(order, k)
					if k == engine.PreparedSQL {
						return failure
					}
					return nil
				}})
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
			}
			op.Done()
			switch reason {
			case engine.CleanClose:
				if err := c.Close(); !errors.Is(err, failure) {
					t.Fatal("Close cleanup error", err)
				}
			case engine.Terminated:
				s.RequestEnd(engine.Terminated)
			case engine.Abandoned:
				cleanupSession(cleanupCapsule{s})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.WaitEnd(ctx); !errors.Is(err, failure) {
				t.Fatal("WaitEnd cleanup error", err)
			}
			if s.IsLive() || s.TransactionState() != engine.TxIdle || c.IsValid() {
				t.Fatal("terminal session remained live/active/valid")
			}
			want := []engine.ResourceKind{engine.PreparedSQL, engine.Temporary}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("resource drain order", order, want)
			}
			s.RequestEnd(engine.CleanClose)
			s.RequestEnd(engine.Terminated)
			s.RequestEnd(engine.Abandoned)
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
	if c.session.TransactionState() != engine.TxActive {
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
	if c.session.TransactionState() != engine.TxActive || c.session.Token() != token || !c.IsValid() {
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
	if c.session.IsLive() || c.session.TransactionState() != engine.TxIdle {
		t.Fatal("physical close did not end transaction")
	}
}
