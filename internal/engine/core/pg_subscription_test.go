package core_test

import (
	"errors"
	"testing"
)

func subscriptionInvariant(t *testing.T, nilRows bool, err error) {
	t.Helper()
	if !nilRows || err == nil {
		t.Fatal("invalid capture not atomic", nilRows, err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("invariant fabricated SQLSTATE", err)
	}
}
