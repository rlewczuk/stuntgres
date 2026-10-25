package fixture

import (
	"bytes"
	"stuntgres"
	"testing"
)

func CommentInquiryDump(t *testing.T, p *Pair) string {
	t.Helper()
	var b bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
