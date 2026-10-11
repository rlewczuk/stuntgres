package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceDDLNativeDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func referenceDDLPositionError(query, code, message, position string, prepared bool, stage string) fixture.Step {
	s := fixture.RoleReferenceError(query, code, message, "", prepared, stage)
	s.Diagnostic.Position = position
	s.ErrorStage = stage
	return s
}
