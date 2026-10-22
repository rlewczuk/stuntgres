package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPgcryptoMD5Salt(t *testing.T) {
	var text *Descriptor
	for _, descriptor := range Builtins() {
		if descriptor.ID() == Text {
			text = descriptor
		}
	}
	if text == nil {
		t.Fatal("missing TEXT descriptor")
	}
	plan, err := BindPgcrypto("crypt", text.Usage(), text.Usage(), text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	// Pinned PostgreSQL 18.6 UTF8/C returns the first eight salt bytes,
	// splitting é. The shared SQL fixture requires valid UTF8 TEXT, so pin
	// this raw package result here rather than changing TEXT admission or
	// replacing invalid bytes. The valid eight-byte boundary is public SQL
	// coverage in TestReferencePgcryptoPasswordVectors.
	const want = "$1$aaaaaaa\xc3$Gqh2MpAUuRzXvYxuz1frn1"
	value, err := plan.Evaluate(context.Background(), nil, String(text, "foox"), String(text, "$1$aaaaaaaé"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Text()
	if err != nil || got != want {
		t.Fatalf("raw MD5 salt result=%q want=%q err=%v", got, want, err)
	}
}

func TestPgcryptoMD5Vectors(t *testing.T) {
	// REL_18_6 regression constants and pinned 18.6 UTF8/C edge observations.
	for _, v := range []struct{ password, setting, want string }{
		{"", "$1$Szzz0yzz", "$1$Szzz0yzz$To38XrR3BsbXQW2ZpfKjF1"},
		{"foox", "$1$Szzz0yzz", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1"},
		{"foox", "$1$Szzz0yzzEXTRA", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1"},
		{"foox", "$1$", "$1$$yS/p28w2q0fzatmSApPDM0"},
		{"foox", "$1$$ignored", "$1$$yS/p28w2q0fzatmSApPDM0"},
		{"foox", "$1$!@:", "$1$!@:$7/eL0rrpLEdNF.jbaJ8FL0"},
		{"foox", "$1$aaaaaaé", "$1$aaaaaaé$VTDkaZHxkN.iWiYEiJ8EU0"},
		{"foox\x00tail", "$1$", "$1$$yS/p28w2q0fzatmSApPDM0"},
	} {
		got, err := pgcryptoMD5(context.Background(), v.password, v.setting)
		if err != nil || got != v.want {
			t.Fatalf("password=%q setting=%q got=%q want=%q err=%v", v.password, v.setting, got, v.want, err)
		}
		got, err = pgcryptoMD5(context.Background(), v.password, v.want+"ignored")
		if err != nil || got != v.want {
			t.Fatal("stored MD5 hash", got, v.want, err)
		}
	}
}

func TestPgcryptoMD5Cancellation(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, password := range []string{"password", strings.Repeat("p", 1<<20)} {
			for _, after := range []int{1, 3, 25, 100} {
				ctx := &pgcryptoBcryptCancelContext{Context: context.Background(), after: after, failure: failure}
				got, err := pgcryptoMD5(ctx, password, "$1$salt")
				if got != "" || !errors.Is(err, failure) || ctx.calls != after {
					t.Fatal(len(password), after, got, err, ctx.calls)
				}
			}
		}
	}
}
