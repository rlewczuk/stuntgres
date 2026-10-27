package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/util"
)

func TestPgcryptoBcryptVectors(t *testing.T) {
	// Published PostgreSQL regression and Openwall wrapper.c witnesses, not
	// expected values calculated by this implementation. Raw a3 is not UTF8 SQL.
	for _, v := range []struct{ password, salt, want string }{
		{"", "$2a$06$RQiOJ.3ELirrXwxIZY8q0O", "$2a$06$RQiOJ.3ELirrXwxIZY8q0OlGbBEpDmx7IRZlNYvGJ1SHXwNi2cEKK"},
		{"foox", "$2a$06$RQiOJ.3ELirrXwxIZY8q0O", "$2a$06$RQiOJ.3ELirrXwxIZY8q0OR3CVJrAfda1z26CCHPnB6mmVZD8p0/C"},
		{"U*U", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
		{"\xa3", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.Sa7shbm4.OzKpvFnX1pQLmQW96oUlCq"},
		{"\xa3", "$2x$05$/OK.fbVrR/bpIqNJ5ianF.", "$2x$05$/OK.fbVrR/bpIqNJ5ianF.CE5elHaaO4EbggVDjb8P19RukzXSM3e"},
	} {
		got, err := pgcryptoBcrypt(context.Background(), v.password, v.salt)
		if err != nil || got != v.want {
			t.Fatalf("password=%q salt=%s got=%s want=%s err=%v", v.password, v.salt, got, v.want, err)
		}
	}
}

func TestPgcryptoBcryptPasswordProtocol(t *testing.T) {
	if got := pgcryptoBcryptKey("", false); got != ([18]uint32{}) {
		t.Fatal(got)
	}
	if got := pgcryptoBcryptKey("a", false); got[0] != 0x61006100 || got[17] != 0x61006100 {
		t.Fatal(got)
	}
	if got := pgcryptoBcryptKey("abc", false); got[0] != 0x61626300 || got[17] != 0x61626300 {
		t.Fatal(got)
	}
	if got := pgcryptoBcryptKey("\xa3", true); got[0] != 0xffffa300 {
		t.Fatal(got)
	}
	salt := "$2a$05$6bNw2HLQYeqHYyBfLMsv/O"
	for _, v := range []struct{ password, want string }{
		{strings.Repeat("a", 71), "$2a$05$6bNw2HLQYeqHYyBfLMsv/OG9hFigh6pPaJz1RpKoWaDOdj/xLzsw."},
		{strings.Repeat("a", 72), "$2a$05$6bNw2HLQYeqHYyBfLMsv/OGZ54Ep3QxFFOkY9vPONoxB/E/5Sh8qu"},
		{strings.Repeat("a", 73), "$2a$05$6bNw2HLQYeqHYyBfLMsv/OGZ54Ep3QxFFOkY9vPONoxB/E/5Sh8qu"},
		{strings.Repeat("a", 1<<20), "$2a$05$6bNw2HLQYeqHYyBfLMsv/OGZ54Ep3QxFFOkY9vPONoxB/E/5Sh8qu"},
		{strings.Repeat("a", 71) + "é", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OfBEsYqGKj/1aYz64jky1rImqm9uSq7q"},
		{strings.Repeat("a", 71) + "ê", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OfBEsYqGKj/1aYz64jky1rImqm9uSq7q"},
	} {
		got, err := pgcryptoBcrypt(context.Background(), v.password, salt)
		if err != nil || got != v.want {
			t.Fatal(len(v.password), got, v.want, err)
		}
	}
}

func TestPgcryptoBcryptSaltParsing(t *testing.T) {
	for _, salt := range []string{"$2a$", "$2a$03$......................", "$2x$32$......................", "$2a$04$.....................=", "$2a$04$.....................\r", "$2a$04$.....................\n"} {
		if _, _, err := pgcryptoBcryptSalt(salt); err == nil {
			t.Fatal("invalid salt accepted", salt)
		}
	}
	for _, salt := range []string{"$2a$04$......................", "$2a$04$...................../", "$2a$04$......................arbitrary suffix"} {
		cost, words, err := pgcryptoBcryptSalt(salt)
		if err != nil || cost != 4 || words != ([4]uint32{}) {
			t.Fatal(cost, words, err)
		}
	}
}

func TestPgcryptoBcryptSaltEncoding(t *testing.T) {
	if got := PgcryptoSalt(PgcryptoSaltParameters{kind: pgcryptoSaltBcrypt, rounds: 4}, [16]byte{}); got != "$2a$04$......................" {
		t.Fatal(got)
	}
	// Every 0xff octet encodes six one bits; the final two bits become index48.
	var allOnes [16]byte
	for i := range allOnes {
		allOnes[i] = 255
	}
	if got := PgcryptoSalt(PgcryptoSaltParameters{kind: pgcryptoSaltBcrypt, rounds: 31}, allOnes); got != "$2a$31$999999999999999999999u" {
		t.Fatal(got)
	}
}

var pgcryptoBcryptAllocationSink string

func TestPgcryptoBcryptAllocations(t *testing.T) {
	short, long := "a", strings.Repeat("a", 1<<20)
	type measurement struct{ allocations, bytes int64 }
	measure := func(password, salt string) measurement {
		allocations, bytes := util.AllocationsPerCall(16, func() {
			var err error
			pgcryptoBcryptAllocationSink, err = pgcryptoBcrypt(context.Background(), password, salt)
			if err != nil {
				t.Fatal(err)
			}
		})
		return measurement{allocations, bytes}
	}
	a := measure(short, "$2a$04$......................")
	b := measure(long, "$2a$04$......................")
	c := measure(short, "$2a$05$......................")
	for _, result := range []measurement{a, b, c} {
		if result.bytes > 16384 || result.allocations > 16 {
			t.Fatal("absolute allocation budget", result)
		}
	}
	if b.bytes-a.bytes > 1024 || b.allocations-a.allocations > 2 {
		t.Fatal("input-proportional allocation", a, b)
	}
	if c != a {
		t.Fatal("round-proportional allocation", a, c)
	}
}

type pgcryptoBcryptCancelContext struct {
	context.Context
	calls, after int
	failure      error
}

func (c *pgcryptoBcryptCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return c.failure
	}
	return nil
}

func TestPgcryptoBcryptCancellation(t *testing.T) {
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
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, v := range []struct {
			salt  string
			after int
		}{
			{"$2a$04$......................", 1}, {"$2a$04$......................", 9}, {"$2a$04$......................", 18}, {"$2a$31$......................", 3},
		} {
			ctx := &pgcryptoBcryptCancelContext{Context: context.Background(), after: v.after, failure: failure}
			got, err := pgcryptoBcrypt(ctx, "password", v.salt)
			if got != "" || !errors.Is(err, failure) || ctx.calls != v.after {
				t.Fatal(v, got, err, ctx.calls)
			}
		}
		for _, after := range []int{1, 9, 19} {
			ctx := &pgcryptoBcryptCancelContext{Context: context.Background(), after: after, failure: failure}
			got, err := plan.Evaluate(ctx, nil, String(text, "password"), String(text, "$2a$04$......................"))
			if got.Type() != nil || !errors.Is(err, failure) {
				t.Fatal("canceled plan published Value", after, got, err)
			}
		}
	}
}
