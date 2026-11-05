package sqltype

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"testing"
)

func pgpUnitHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestPgcryptoPGPCAST5(t *testing.T) {
	// RFC2144 Appendix B.1, independent encryption and decryption anchors.
	key := pgpUnitHex(t, "0123456712345678234567893456789a")
	plain := pgpUnitHex(t, "0123456789abcdef")
	encrypted := pgpUnitHex(t, "238b4fe5847e44b2")
	block, err := pgpNewBlock(3, key)
	if err != nil {
		t.Fatal(err)
	}
	defer block.clear()
	for _, inPlace := range []bool{false, true} {
		input := append([]byte(nil), plain...)
		out := make([]byte, 8)
		if inPlace {
			out = input
		}
		block.Encrypt(out, input)
		if !bytes.Equal(out, encrypted) {
			t.Fatalf("CAST encrypt: %x", out)
		}
		input = append([]byte(nil), encrypted...)
		if inPlace {
			out = input
		}
		block.Decrypt(out, input)
		if !bytes.Equal(out, plain) {
			t.Fatalf("CAST decrypt: %x", out)
		}
	}
}
func TestPgcryptoPGPCiphers(t *testing.T) {
	// FIPS197 AES examples; 3DES with all equal DES keys reduces to the
	// standard FIPS46-3 DES example. Existing BF vectors cover its primitive;
	// this zero-key anchor additionally checks the PGP adapter's byte packing.
	for _, v := range []struct {
		id                    pgpCipher
		key, plain, encrypted string
	}{
		{7, "000102030405060708090a0b0c0d0e0f", "00112233445566778899aabbccddeeff", "69c4e0d86a7b0430d8cdb78070b4c55a"},
		{8, "000102030405060708090a0b0c0d0e0f1011121314151617", "00112233445566778899aabbccddeeff", "dda97ca4864cdfe06eaf70a0ec0d7191"},
		{9, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "00112233445566778899aabbccddeeff", "8ea2b7ca516745bfeafc49904b496089"},
		{2, "133457799bbcdff1133457799bbcdff1133457799bbcdff1", "0123456789abcdef", "85e813540f0ab405"},
		{4, "00000000000000000000000000000000", "0000000000000000", "4ef997456198dd78"},
	} {
		block, err := pgpNewBlock(v.id, pgpUnitHex(t, v.key))
		if err != nil {
			t.Fatal(err)
		}
		plain, encrypted := pgpUnitHex(t, v.plain), pgpUnitHex(t, v.encrypted)
		size, bs := pgpCipherSize(v.id)
		if size != len(v.key)/2 || bs != len(plain) || block.BlockSize() != bs {
			t.Fatal("cipher sizes", v.id)
		}
		got := make([]byte, bs)
		block.Encrypt(got, plain)
		if !bytes.Equal(got, encrypted) {
			t.Fatalf("cipher%d encrypt %x", v.id, got)
		}
		block.Decrypt(got, encrypted)
		if !bytes.Equal(got, plain) {
			t.Fatalf("cipher%d decrypt %x", v.id, got)
		}
		block.clear()
	}
	if _, err := pgpNewBlock(10, make([]byte, 32)); err == nil {
		t.Fatal("unsupported cipher accepted")
	}
}

func pgpUnitS2KProfiles(t *testing.T) {
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	for _, digest := range []struct {
		id  pgpDigest
		new func() hash.Hash
	}{{1, md5.New}, {2, sha1.New}, {8, sha256.New}} {
		for _, mode := range []int{0, 1, 3} {
			for _, size := range []int{16, 24, 32} {
				password := "independent password"
				unit := []byte(password)
				if mode != 0 {
					unit = append(append([]byte(nil), salt...), unit...)
				}
				count := len(unit)
				if mode == 3 {
					count = max(1024, count)
				}
				material := bytes.Repeat(unit, (count+len(unit)-1)/len(unit))[:count]
				var want []byte
				for stream := 0; len(want) < size; stream++ {
					h := digest.new()
					h.Write(make([]byte, stream))
					h.Write(material)
					want = h.Sum(want)
				}
				got := bytes.Repeat([]byte{255}, size)
				if err := pgpS2KKey(context.Background(), got, password, salt, mode, digest.id, 0); err != nil || !bytes.Equal(got, want[:size]) {
					t.Fatal("S2K mode/hash/multidigest", mode, digest.id, size, err)
				}
			}
		}
	}
}

func pgpUnitCFBProfiles(t *testing.T) {
	for _, size := range []int{8, 16} {
		var reference cipher.Block
		var id pgpCipher
		if size == 16 {
			id = 7
			reference, _ = aes.NewCipher(make([]byte, 16))
		} else {
			id = 2
			reference, _ = des.NewTripleDESCipher(make([]byte, 24))
		}
		keySize, _ := pgpCipherSize(id)
		block, err := pgpNewBlock(id, make([]byte, keySize))
		if err != nil {
			t.Fatal(err)
		}
		for _, resync := range []bool{false, true} {
			for _, length := range []int{0, 1, size - 1, size, size + 1, 16385} {
				input := make([]byte, size+2+length)
				for i := range input {
					input[i] = byte(i*31 + 7)
				}
				input[size], input[size+1] = input[size-2], input[size-1]
				// Independent RFC13.9 author: encrypt prefix+2, then start a NEW CFB
				// stream on ciphertext[2:block+2]. Expected is never a roundtrip result.
				want := make([]byte, len(input))
				if resync {
					cipher.NewCFBEncrypter(reference, make([]byte, size)).XORKeyStream(want[:size+2], input[:size+2])
					cipher.NewCFBEncrypter(reference, want[2:size+2]).XORKeyStream(want[size+2:], input[size+2:])
				} else {
					cipher.NewCFBEncrypter(reference, make([]byte, size)).XORKeyStream(want, input)
				}
				got := append([]byte(nil), input...)
				if err = pgpCFB(context.Background(), block.Block, got, false, resync); err != nil || !bytes.Equal(got, want) {
					t.Fatal("independent CFB emission", size, resync, length, err)
				}
				got = append(got[:0], want...)
				if err = pgpCFB(context.Background(), block.Block, got, true, resync); err != nil || !bytes.Equal(got, input) {
					t.Fatal("independent CFB decrypt", size, resync, length, err)
				}
			}
		}
		block.clear()
	}
}
