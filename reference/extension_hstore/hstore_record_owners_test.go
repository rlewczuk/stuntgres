package reference_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Record-role corruption uses the same complete symbolic inventory as the
// incumbent hstore controls, including the omitted ownerless cast.
func hstoreRecordRejectOwner(t *testing.T, p *fixture.Pair, dump []byte, phase string) {
	t.Helper()
	kind, ok := map[string]string{"owner_constructor": "signature_51", "owner_replacement": "signature_52", "owner_operator": "signature_53"}[phase]
	if !ok {
		t.Fatal("unknown record owner mutation", phase)
	}
	bad := hstoreInvalidOwnerDump(t, dump, kind)
	err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	var state interface{ SQLState() string }
	if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
		t.Fatalf("ordinary owner rejection: %v", err)
	}
	if !bytes.Equal(dump, hstoreDump(t, p, p.Native)) {
		t.Fatal("rejected record owner changed incumbent dump")
	}
}
