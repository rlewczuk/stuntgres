package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
)

func TestFDWProviderContractErrors(t *testing.T) {
	for _, sentinel := range []error{driver.ErrBadConn, driver.ErrSkip} {
		for _, original := range []error{sentinel, fmt.Errorf("wrapped transport: %w", sentinel), errors.Join(errors.New("joined transport"), sentinel), errors.Join(driver.ErrBadConn, driver.ErrSkip)} {
			got := exec.ForeignCallbackError(original)
			if got == original || errors.Is(got, driver.ErrBadConn) || errors.Is(got, driver.ErrSkip) || !strings.Contains(got.Error(), original.Error()) {
				t.Fatal("replay classification escaped", original, got)
			}
			var state interface{ SQLState() string }
			if errors.As(got, &state) || errors.Unwrap(got) != nil {
				t.Fatal("contract violation invented diagnostics/unwrap", got)
			}
		}
	}
	ordinary := errors.New("ordinary provider failure")
	diagnostic := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0001", Message: "provider SQL diagnostic"}
	for _, original := range []error{nil, ordinary, diagnostic, context.Canceled, context.DeadlineExceeded, errors.Join(ordinary, context.Canceled)} {
		if got := exec.ForeignCallbackError(original); got != original {
			t.Fatal("ordinary error identity changed", original, got)
		}
	}
}
