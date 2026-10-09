package stuntgres_test

import (
	"errors"
	"stuntgres"
	"testing"
)

func typeCoreDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}
