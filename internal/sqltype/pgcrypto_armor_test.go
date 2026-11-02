package sqltype

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func armorUnitTypes() map[ID]*Descriptor {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	return types
}

func armorUnitPlan(t *testing.T, name string, inputs ...ID) (PgcryptoPlan, map[ID]*Descriptor) {
	t.Helper()
	types := armorUnitTypes()
	args := make([]Usage, len(inputs))
	for i, id := range inputs {
		args[i] = types[id].Usage()
	}
	result := Text
	if name == "dearmor" {
		result = Bytea
	}
	p, err := BindPgcrypto(name, types[result].Usage(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return p, types
}

func TestPgcryptoArmorKernelVectors(t *testing.T) {
	p, types := armorUnitPlan(t, "armor", Bytea)
	for _, tc := range []struct {
		data, frame string
		crc         uint32
	}{
		{"", "-----BEGIN PGP MESSAGE-----\n\n=twTO\n-----END PGP MESSAGE-----\n", 0xb704ce},
		{"test", "-----BEGIN PGP MESSAGE-----\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n", 0xf86ed0},
		{"123456789", "-----BEGIN PGP MESSAGE-----\n\nMTIzNDU2Nzg5\n=Ic8C\n-----END PGP MESSAGE-----\n", 0x21cf02},
	} {
		data, err := Binary(types[Bytea], tc.data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(context.Background(), nil, nil, data)
		text, _ := got.Text()
		if err != nil || text != tc.frame {
			t.Fatalf("armor(%q)=%q: %v", tc.data, text, err)
		}
		crc, err := armorCRC(context.Background(), tc.data)
		if err != nil || crc != tc.crc {
			t.Fatal(crc, tc.crc, err)
		}
		decoded, err := PgcryptoDearmor(context.Background(), tc.frame)
		if err != nil || decoded != tc.data {
			t.Fatal(decoded, err)
		}
	}
	for _, size := range []int{1, 2, 3, 56, 57, 58, 114, 115, 256} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i)
		}
		value, _ := Binary(types[Bytea], string(data))
		got, err := p.Evaluate(context.Background(), nil, nil, value)
		text, _ := got.Text()
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(text, "\n")
		var body strings.Builder
		for _, line := range lines[2 : len(lines)-3] {
			if len(line) == 0 || len(line) > 76 {
				t.Fatal("physical wrap", size, lines)
			}
			body.WriteString(line)
		}
		if body.String() != base64.StdEncoding.EncodeToString(data) {
			t.Fatal("binary body", size, text)
		}
		decoded, err := PgcryptoDearmor(context.Background(), text)
		if err != nil || decoded != string(data) {
			t.Fatal("binary decode", size, err)
		}
	}
}

func TestPgcryptoArmorParserModes(t *testing.T) {
	ctx := context.Background()
	packet := "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\nem9va2E=\n=D5cR\n-----END PGP MESSAGE-----\n"
	for _, text := range []string{packet, strings.ReplaceAll(packet, "\n", "\r\n"), strings.TrimSuffix(packet, "\n"), "ignored\n" + packet + "ignored", strings.ReplaceAll(strings.ReplaceAll(packet, "BEGIN PGP MESSAGE", "BEGIN generic"), "END PGP MESSAGE", "END different")} {
		got, err := PgcryptoDearmor(ctx, text)
		if err != nil || got != "zooka" {
			t.Fatal(got, err)
		}
	}
	for _, text := range []string{
		strings.Replace(packet, "=D5cR", "=ZZZZ", 1),
		strings.Replace(packet, "em9va2E=\n=D5cR", "!not-base64!", 1),
		"-----BEGIN PGP MESSAGE-----\nfoo: bar\n-----END PGP MESSAGE-----\n",
	} {
		start, stop, err := PgcryptoArmorHeaderRegion(ctx, text)
		if err != nil || text[start:stop] != "foo: bar\n" {
			t.Fatal(start, stop, err)
		}
		if _, err := PgcryptoDearmor(ctx, text); err == nil {
			t.Fatal("decoder accepted bad body/CRC")
		}
	}
	badHeader := strings.Replace(packet, "foo: bar", "invalid header", 1)
	if got, err := PgcryptoDearmor(ctx, badHeader); err != nil || got != "zooka" {
		t.Fatal(got, err)
	}
	if _, _, err := PgcryptoArmorHeaderRegion(ctx, badHeader); err == nil {
		t.Fatal("invalid header admitted")
	}
	for _, text := range []string{"", " " + packet, strings.Replace(packet, "=D5cR", "=D5c!", 1), strings.Replace(packet, "-----END", "x-----END", 1)} {
		_, err := PgcryptoDearmor(ctx, text)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "39000" || diagnostic.Message != "Corrupt ascii-armor" {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := PgcryptoDearmor(canceled, packet); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPgcryptoArmorArrayRecipe(t *testing.T) {
	p, types := armorUnitPlan(t, "armor", Bytea, 1009, 1009)
	binary, _ := Binary(types[Bytea], "zooka")
	array := func(s string) Value {
		t.Helper()
		v, err := types[1009].Input(context.Background(), s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	keys, vals := array("[0:0]={foo}"), array("[7:7]={bar}")
	backing := keys.array
	got, err := p.Evaluate(context.Background(), nil, nil, binary, keys, vals)
	text, _ := got.Text()
	if err != nil || text != "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\nem9va2E=\n=D5cR\n-----END PGP MESSAGE-----\n" || keys.array != backing {
		t.Fatal(text, err)
	}
	for _, tc := range []struct{ keys, values, code, message string }{
		{"{}", "{v}", "2202E", "wrong number of array subscripts"},
		{"{{k}}", "{{v}}", "2202E", "wrong number of array subscripts"},
		{"{k}", "{v,v}", "2202E", "mismatched array dimensions"},
		{"{NULL}", "{NULL}", "22004", "null value not allowed for header key"},
		{"{k}", "{NULL}", "22004", "null value not allowed for header value"},
		{"{é}", "{v}", "22023", "header key must not contain non-ASCII characters"},
		{"{k}", "{é}", "22023", "header value must not contain non-ASCII characters"},
		{"{\"k: bad\"}", "{v}", "22023", "header key must not contain \": \""},
		{"{\"k\n\"}", "{v}", "22023", "header key must not contain newlines"},
		{"{k,badé}", "{NULL,v}", "22004", "null value not allowed for header value"},
	} {
		_, err := p.Evaluate(context.Background(), nil, nil, binary, array(tc.keys), array(tc.values))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc, err)
		}
	}
	got, err = p.Evaluate(context.Background(), nil, nil, binary, Null(types[1009]), array("{{bad}}"))
	if err != nil || !got.IsNull() || got.Type() != types[Text] {
		t.Fatal(got, err)
	}
	if p.Signature().SQL != "17,1009,1009" || p.Signature().Inputs != [4]ID{Bytea, 1009, 1009} {
		t.Fatal(p.Signature())
	}
	if _, err := BindPgcrypto("armor", types[Text].Usage(), types[Bytea].Usage(), types[Text].Usage(), types[Text].Usage()); err == nil {
		t.Fatal("wrong formals accepted")
	}
}
