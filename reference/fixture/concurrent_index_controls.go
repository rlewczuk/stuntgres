package fixture

import (
	"stuntgres"
	"testing"
)

type ConcurrentIndexCountingWriter struct{ Calls, Bytes int }

func (w *ConcurrentIndexCountingWriter) Write(p []byte) (int, error) {
	w.Calls++
	w.Bytes += len(p)
	return len(p), nil
}

func ConcurrentIndexDumpInvalid(t *testing.T, p *Pair, name string) {
	t.Helper()
	before, lookupErr := stuntgres.InspectIndex(p.Ctx, p.Native, "public", name)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	var out ConcurrentIndexCountingWriter
	err := stuntgres.Dump(p.Ctx, p.Native, &out)
	want := "stuntgres: dump cannot serialize invalid index \"public\".\"" + name + "\""
	if err == nil || err.Error() != want || out.Calls != 0 || out.Bytes != 0 {
		t.Fatalf("invalid Dump: %v calls=%d bytes=%d", err, out.Calls, out.Bytes)
	}
	after, lookupErr := stuntgres.InspectIndex(p.Ctx, p.Native, "public", name)
	if lookupErr != nil || after != before {
		t.Fatal("Dump changed state", lookupErr, before, after)
	}
}
