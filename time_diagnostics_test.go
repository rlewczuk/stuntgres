package stuntgres_test

import (
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func timeDiagnostic(t *testing.T, err error, want stuntgres.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic %#v (%v) want %#v", got, err, want)
	}
}
