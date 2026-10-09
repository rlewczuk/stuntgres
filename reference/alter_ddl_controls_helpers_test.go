package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func referenceDDLNativeDump(t *testing.T, p *referencePair) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func referenceDDLPositionError(query, code, message, position string, prepared bool, stage string) referenceStep {
	s := roleReferenceError(query, code, message, "", prepared, stage)
	s.diagnostic.Position = position
	s.errorStage = stage
	return s
}
