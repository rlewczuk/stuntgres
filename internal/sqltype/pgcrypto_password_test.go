package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func pgcryptoSaltUnitPlan(t *testing.T, explicit bool) (PgcryptoPlan, *Descriptor, *Descriptor) {
	t.Helper()
	var text, integer *Descriptor
	for _, descriptor := range Builtins() {
		switch descriptor.ID() {
		case Text:
			text = descriptor
		case Int4:
			integer = descriptor
		}
	}
	args := []Usage{text.Usage()}
	if explicit {
		args = append(args, integer.Usage())
	}
	plan, err := BindPgcrypto("gen_salt", text.Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return plan, text, integer
}

func TestPgcryptoSaltParameters(t *testing.T) {
	plan, text, integer := pgcryptoSaltUnitPlan(t, true)
	for _, v := range []struct {
		name  string
		count int64
		want  uint32
		size  int
	}{
		{"des", 0, 25, 2}, {"DeS", 25, 25, 2}, {"md5", 0, 1000, 6}, {"MD5", 1000, 1000, 6},
		{"xdes", 0, 725, 3}, {"xdes", 1, 1, 3}, {"xdes", 3, 3, 3}, {"xdes", 16777215, 16777215, 3},
		{"bf", 0, 6, 16}, {"BF", 4, 4, 16}, {"bf", 31, 31, 16},
	} {
		count, err := Integer(integer, v.count)
		if err != nil {
			t.Fatal(err)
		}
		parameters, null, err := plan.PrepareSalt(String(text, v.name), count)
		if err != nil || null || parameters.rounds != v.want || parameters.EntropySize() != v.size {
			t.Fatal(v, parameters, null, err)
		}
	}
	for _, name := range []string{"des", "xdes", "md5", "bf"} {
		omitted, text, _ := pgcryptoSaltUnitPlan(t, false)
		parameters, null, err := omitted.PrepareSalt(String(text, name))
		if err != nil || null || parameters.rounds == 0 {
			t.Fatal(name, parameters, null, err)
		}
	}
	for _, v := range []struct {
		name   string
		counts []int64
	}{
		{"des", []int64{-2147483648, -1, 1, 24, 26, 2147483647}},
		{"md5", []int64{-2147483648, -1, 1, 999, 1001, 2147483647}},
		{"xdes", []int64{-2147483648, -1, 2, 724, 16777214, 16777216, 2147483647}},
		{"bf", []int64{-2147483648, -1, 3, 32, 2147483647}},
	} {
		for _, raw := range v.counts {
			count, _ := Integer(integer, raw)
			parameters, null, err := plan.PrepareSalt(String(text, v.name), count)
			var diagnostic *sqlstate.Error
			if null || parameters != (PgcryptoSaltParameters{}) || !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "gen_salt: Incorrect number of rounds" {
				t.Fatal(v.name, raw, parameters, null, err)
			}
		}
	}
	for _, name := range []string{" des", "md5 ", "unknown", "sha256crypt", "sha512crypt"} {
		count, _ := Integer(integer, 1)
		_, _, err := plan.PrepareSalt(String(text, name), count)
		var diagnostic *sqlstate.Error
		code, message := "22023", "gen_salt: Unknown salt algorithm"
		if name == "sha256crypt" || name == "sha512crypt" {
			code, message = "0A000", "gen_salt: unsupported salt algorithm"
		}
		if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
			t.Fatal(name, err)
		}
	}
	invalid, _ := Integer(integer, -1)
	parameters, null, err := plan.PrepareSalt(Null(text), invalid)
	if err != nil || !null || parameters != (PgcryptoSaltParameters{}) {
		t.Fatal(parameters, null, err)
	}
}

func TestPgcryptoSaltEncoding(t *testing.T) {
	var ones [16]byte
	for i := range ones {
		ones[i] = 255
	}
	for _, v := range []struct {
		kind       pgcryptoSaltKind
		rounds     uint32
		zero, ones string
	}{
		{pgcryptoSaltDES, 25, "..", "zz"},
		{pgcryptoSaltXDES, 1, "_/.......", "_/...zzzz"},
		{pgcryptoSaltXDES, 725, "_J9......", "_J9..zzzz"},
		{pgcryptoSaltXDES, 16777215, "_zzzz....", "_zzzzzzzz"},
		{pgcryptoSaltMD5, 1000, "$1$........", "$1$zzzzzzzz"},
		{pgcryptoSaltBcrypt, 4, "$2a$04$......................", "$2a$04$999999999999999999999u"},
	} {
		parameters := PgcryptoSaltParameters{kind: v.kind, rounds: v.rounds}
		if got := PgcryptoSalt(parameters, [16]byte{}); got != v.zero {
			t.Fatal(v, got)
		}
		if got := PgcryptoSalt(parameters, ones); got != v.ones {
			t.Fatal(v, got)
		}
	}
	// Distinct byte positions pin little-endian encoding, not just all-zero/one.
	entropy := [16]byte{1, 2, 3, 4, 5, 6}
	if got := PgcryptoSalt(PgcryptoSaltParameters{kind: pgcryptoSaltXDES, rounds: 725}, entropy); got != "_J9../6k." {
		t.Fatal(got)
	}
	if got := PgcryptoSalt(PgcryptoSaltParameters{kind: pgcryptoSaltMD5, rounds: 1000}, entropy); got != "$1$/6k.2IU/" {
		t.Fatal(got)
	}
}

func TestPgcryptoPasswordDispatch(t *testing.T) {
	for _, v := range []struct{ setting, want, code string }{
		{"ab", "abJnggxhB/yWI", ""},
		{"$2b$04$......................", "$2SXu7evaw9i.", ""},
		{"$2y$04$......................", "$2SXu7evaw9i.", ""},
		{"_0.......", "_0.......JdXyN0hVWiM", ""},
		{"$2$", "", "39000"}, {"$2a$", "", "22023"}, {"$2x$", "", "22023"},
		{"$5$salt", "", "0A000"}, {"$6$salt", "", "0A000"},
	} {
		got, err := pgcryptoCrypt(context.Background(), "password", v.setting)
		if v.code == "" {
			if err != nil || got != v.want {
				t.Fatal(v, got, err)
			}
		} else {
			var diagnostic *sqlstate.Error
			if got != "" || !errors.As(err, &diagnostic) || diagnostic.Code != v.code {
				t.Fatal(v, got, err)
			}
		}
	}
}
