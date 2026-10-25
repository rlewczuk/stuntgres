package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func sequenceOwnerDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}
