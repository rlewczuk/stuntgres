package engine

import (
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

func TestFDWAsyncAdmissionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name string
		host *StatementHost
		want bool
	}{
		{name: "detached"},
		{name: "internal_scope", host: NewStatementHost(StatementHostFields{})},
		{name: "unadmitted_operation", host: NewStatementHost(StatementHostFields{Op: &session.Operation{AsyncAppend: true}})},
		{name: "admitted_disabled", host: NewStatementHost(StatementHostFields{Op: &session.Operation{CommandAdmission: true}})},
		{name: "admitted_enabled", host: NewStatementHost(StatementHostFields{Op: &session.Operation{CommandAdmission: true, AsyncAppend: true}}), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := exec.State{Source: &exec.Source{Host: tc.host}}
			state.CaptureAsyncAdmission()
			if state.AsyncAppend != tc.want {
				t.Fatalf("admission=%v want %v", state.AsyncAppend, tc.want)
			}
			if tc.host != nil && tc.host.Op != nil {
				tc.host.Op.AsyncAppend = !tc.host.Op.AsyncAppend
			}
			state.CaptureAsyncAdmission()
			if state.AsyncAppend != tc.want {
				t.Fatal("retained state reread a later admission")
			}
		})
	}
}
