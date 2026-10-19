package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPgcryptoDESVectors(t *testing.T) {
	// Published REL_18_6 regressions and pinned 18.6 observations (tests-1).
	for _, v := range []struct{ password, setting, want string }{
		{"", "NB", "NBPx/38Y48kHg"},
		{"foox", "NB", "NB53EGGqrrb5E"},
		{"password", "ab", "abJnggxhB/yWI"},
		{"aaaaaaaé", "NB", "NBqsnep6jbEUQ"},
		{"", "_J9..j2zz", "_J9..j2zzR/nIRDK3pPc"},
		{"foox", "_J9..j2zz", "_J9..j2zzAYKMvO2BYRY"},
		{"longlongpassword", "_J9..j2zz", "_J9..j2zz4BeseiQNwUg"},
		{"abcdefghijklmnopq", "_J9..j2zz", "_J9..j2zzMajpe2REvSE"},
		{"abcdefghijklmnopr", "_J9..j2zz", "_J9..j2zzG8w48ydCb3c"},
		{"password", "_/!!!!!!!", "_/!!!!!!!zqM49hRzxko"},
		{"password", "_0.......", "_0.......JdXyN0hVWiM"},
	} {
		got, err := pgcryptoDES(context.Background(), v.password, v.setting)
		if err != nil || got != v.want {
			t.Fatalf("password=%q setting=%q got=%q want=%q err=%v", v.password, v.setting, got, v.want, err)
		}
		got, err = pgcryptoDES(context.Background(), v.password, v.want+"ignored")
		if err != nil || got != v.want {
			t.Fatalf("stored hash reuse: got=%q want=%q err=%v", got, v.want, err)
		}
	}
}

func TestPgcryptoDESProtocol(t *testing.T) {
	// FIPS 46-3 ordinary DES known answer supplements salted crypt vectors.
	keys := pgcryptoDESKeys(0x133457799bbcdff1)
	block, err := pgcryptoDESBlock(context.Background(), 0x0123456789abcdef, &keys, 0, 1)
	if err != nil || block != 0x85e813540f0ab405 {
		t.Fatalf("DES primitive=%016x err=%v", block, err)
	}
	for _, pair := range [][2]string{
		{"abcdefgh", "abcdefghi"}, {"aaaaaaaé", "aaaaaaaê"},
		{"password", "\xf0\xe1\xf3\xf3\xf7\xef\xf2\xe4"}, {"foox", "foox\x00tail"},
	} {
		a, err := pgcryptoDES(context.Background(), pair[0], "NB")
		if err != nil {
			t.Fatal(err)
		}
		b, err := pgcryptoDES(context.Background(), pair[1], "NB")
		if err != nil || a != b {
			t.Fatal(pair, a, b, err)
		}
	}
	seven, _ := pgcryptoDES(context.Background(), "abcdefg", "NB")
	eight, _ := pgcryptoDES(context.Background(), "abcdefgh", "NB")
	if seven == eight {
		t.Fatal("eighth byte did not affect DES")
	}
	for _, setting := range []string{"", "x", "_", "_1234567", "_........"} {
		_, err := pgcryptoDES(context.Background(), "p", setting)
		var diagnostic *sqlstate.Error
		code, message := "22023", "invalid salt"
		if setting == "_........" {
			code, message = "39000", "crypt(3) returned NULL"
		}
		if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
			t.Fatal(setting, err)
		}
	}
	if got := pgcryptoCryptDecode("zzzz"); got != 0xffffff {
		t.Fatalf("24-bit count=%x", got)
	}
	if got := pgcryptoCryptDecode("/!!!"); got != 1 {
		t.Fatal(got)
	}
	// Concurrent settings/key schedules must not share mutable scratch.
	for _, v := range []struct{ password, setting, want string }{
		{"password", "ab", "abJnggxhB/yWI"},
		{"foox", "_J9..j2zz", "_J9..j2zzAYKMvO2BYRY"},
		{"abcdefghijklmnopq", "_J9..j2zz", "_J9..j2zzMajpe2REvSE"},
	} {
		t.Run(v.password, func(t *testing.T) {
			t.Parallel()
			for range 3 {
				got, err := pgcryptoDES(context.Background(), v.password, v.setting)
				if err != nil || got != v.want {
					t.Fatal(v, got, err)
				}
			}
		})
	}
}

func TestPgcryptoDESCancellation(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, v := range []struct {
			password, setting string
			after             int
		}{
			{"p", "NB", 1}, {"p", "NB", 12}, {"p", "NB", 27},
			{"p", "_zzzz....", 8}, {strings.Repeat("p", 256), "_/.......", 10},
		} {
			ctx := &pgcryptoBcryptCancelContext{Context: context.Background(), after: v.after, failure: failure}
			got, err := pgcryptoDES(ctx, v.password, v.setting)
			if got != "" || !errors.Is(err, failure) || ctx.calls != v.after {
				t.Fatal(v, got, err, ctx.calls)
			}
		}
	}
}
