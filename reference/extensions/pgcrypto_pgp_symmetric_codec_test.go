package reference_test

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"testing"

	"golang.org/x/crypto/blowfish"
	"golang.org/x/crypto/cast5"
)

// Independent test oracle: RFC 4880 §§3.7,5.3,5.7,5.9,5.13 and 13.9.
// Legacy block primitives are test-only upstream x/crypto, never production
// implementations. Deterministic randomness below is fixture material only.
type symProfile struct {
	cipher, wrap, digest, mode byte
	count                      byte
	session, noMDC             bool
	compression                byte
}

func symDefault() symProfile { return symProfile{cipher: 7, wrap: 7, digest: 2, mode: 3} }

func symKeySize(id byte) int {
	if size := symKeySizeKnown(id); size != 0 {
		return size
	}
	panic("fixture cipher ID")
}

func symBlock(id byte, key []byte) (cipher.Block, error) {
	switch id {
	case 2:
		return des.NewTripleDESCipher(key)
	case 3:
		return cast5.NewCipher(key)
	case 4:
		return blowfish.NewCipher(key)
	case 7, 8, 9:
		return aes.NewCipher(key)
	}
	return nil, fmt.Errorf("unknown fixture cipher %d", id)
}

func symKey(password string, salt []byte, mode, digest byte, count, size int) []byte {
	var newHash func() hash.Hash
	switch digest {
	case 1:
		newHash = md5.New
	case 2:
		newHash = sha1.New
	case 8:
		newHash = sha256.New
	default:
		panic("fixture digest ID")
	}
	unit := append(append([]byte(nil), salt...), password...)
	defer clear(unit)
	if mode == 0 {
		unit = []byte(password)
		defer clear(unit)
	}
	if mode != 3 {
		count = len(unit)
	}
	count = max(count, len(unit))
	// Feed repeated units in chunks without allocating count-sized scratch.
	chunk := make([]byte, max(4096, len(unit)))
	defer clear(chunk)
	for i := range chunk {
		chunk[i] = unit[i%len(unit)]
	}
	key := make([]byte, 0, size)
	for stream := 0; len(key) < size; stream++ {
		h := newHash()
		h.Write(make([]byte, stream))
		left, offset := count, 0
		for left > 0 {
			n := min(left, len(chunk))
			// Chunk length need not be a multiple of the password unit.
			for i := range n {
				chunk[i] = unit[(offset+i)%len(unit)]
			}
			h.Write(chunk[:n])
			offset = (offset + n) % len(unit)
			left -= n
		}
		sum := h.Sum(nil)
		key = append(key, sum[:min(size-len(key), len(sum))]...)
		clear(sum)
	}
	return key
}

// The tag9 resynchronization step discards the first two ciphertext octets
// from the feedback register after the block+2 prefix (RFC4880 §13.9).
func symCFB(block cipher.Block, input []byte, decrypt, resync bool) []byte {
	out := make([]byte, len(input))
	bs := block.BlockSize()
	if !resync {
		if decrypt {
			cipher.NewCFBDecrypter(block, make([]byte, bs)).XORKeyStream(out, input)
		} else {
			cipher.NewCFBEncrypter(block, make([]byte, bs)).XORKeyStream(out, input)
		}
		return out
	}
	if len(input) < bs+2 {
		panic("short fixture resync prefix")
	}
	feedback, mask := make([]byte, bs), make([]byte, bs)
	block.Encrypt(mask, feedback)
	for i := range bs {
		out[i] = input[i] ^ mask[i]
	}
	if decrypt {
		copy(feedback, input[:bs])
	} else {
		copy(feedback, out[:bs])
	}
	block.Encrypt(mask, feedback)
	out[bs], out[bs+1] = input[bs]^mask[0], input[bs+1]^mask[1]
	if decrypt {
		copy(feedback, input[2:bs+2])
	} else {
		copy(feedback, out[2:bs+2])
	}
	if decrypt {
		cipher.NewCFBDecrypter(block, feedback).XORKeyStream(out[bs+2:], input[bs+2:])
	} else {
		cipher.NewCFBEncrypter(block, feedback).XORKeyStream(out[bs+2:], input[bs+2:])
	}
	return out
}

func symCompress(inner []byte, algorithm byte, level int) []byte {
	if algorithm == 0 {
		return pgpRFCFrame(8, append([]byte{0}, inner...), "partial")
	}
	var b bytes.Buffer
	var w io.WriteCloser
	var err error
	if algorithm == 1 {
		w, err = flate.NewWriter(&b, level)
	} else {
		w, err = zlib.NewWriterLevel(&b, level)
	}
	if err != nil {
		panic(err)
	}
	if _, err = w.Write(inner); err != nil {
		panic(err)
	}
	if err = w.Close(); err != nil {
		panic(err)
	}
	return pgpRFCFrame(8, append([]byte{algorithm}, b.Bytes()...), "partial")
}

func symBuild(password string, inner []byte, p symProfile, corrupt string) []byte {
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	keyBody := []byte{4, p.wrap, p.mode, p.digest}
	if p.mode != 0 {
		keyBody = append(keyBody, salt...)
	} else {
		salt = nil
	}
	if p.mode == 3 {
		keyBody = append(keyBody, p.count)
	}
	key := symKey(password, salt, p.mode, p.digest, pgpRFCCount(p.count), symKeySize(p.wrap))
	defer clear(key)
	dataKey, dataCipher := key, p.wrap
	if p.session {
		dataCipher = p.cipher
		dataKey = bytes.Repeat([]byte{0x59}, symKeySize(dataCipher))
		defer clear(dataKey)
		session := append([]byte{dataCipher}, dataKey...)
		if corrupt == "session-algorithm" {
			session[0] = 127
		}
		if corrupt == "session-length" {
			session = session[:len(session)-1]
		}
		block, err := symBlock(p.wrap, key)
		if err != nil {
			panic(err)
		}
		keyBody = append(keyBody, symCFB(block, session, false, false)...)
		clear(session)
	}
	block, err := symBlock(dataCipher, dataKey)
	if err != nil {
		panic(err)
	}
	bs := block.BlockSize()
	plain := make([]byte, bs+2)
	for i := range bs {
		plain[i] = byte(21 + i)
	}
	copy(plain[bs:], plain[bs-2:bs])
	if corrupt == "prefix" {
		plain[bs+1] ^= 1
	}
	plain = append(plain, inner...)
	defer func() { clear(plain) }()
	tag := byte(9)
	if !p.noMDC {
		tag = 18
		plain = append(plain, 211, 20)
		sum := sha1.Sum(plain)
		plain = append(plain, sum[:]...)
		if corrupt == "mdc" {
			plain[len(plain)-1] ^= 1
		}
	}
	body := symCFB(block, plain, false, p.noMDC)
	if !p.noMDC {
		body = append([]byte{1}, body...)
	}
	return append(pgpRFCFrame(3, keyBody, ""), pgpRFCFrame(tag, body, "partial")...)
}

type symDecoded struct {
	profile                                  symProfile
	data                                     []byte
	formats                                  []byte
	keySize, saltSize, count, filenameLength int
	partial                                  bool
}

func symDecode(packet []byte, password string) (symDecoded, error) {
	var d symDecoded
	keyPacket, rest, err := pgpRFCRead(packet, false)
	if err != nil {
		return d, err
	}
	k := keyPacket.body
	if keyPacket.tag != 3 || len(k) < 4 || k[0] != 4 {
		return d, fmt.Errorf("SKESK header")
	}
	if symKeySizeKnown(k[1]) == 0 || (k[2] != 0 && k[2] != 1 && k[2] != 3) || (k[3] != 1 && k[3] != 2 && k[3] != 8) {
		return d, fmt.Errorf("unsupported emitted SKESK")
	}
	d.profile.wrap, d.profile.mode, d.profile.digest = k[1], k[2], k[3]
	pos := 4
	var salt []byte
	if k[2] != 0 {
		if len(k) < pos+8 {
			return d, fmt.Errorf("salt")
		}
		salt = k[pos : pos+8]
		pos += 8
		d.saltSize = 8
	}
	if k[2] == 3 {
		if len(k) <= pos {
			return d, fmt.Errorf("count")
		}
		d.profile.count = k[pos]
		d.count = pgpRFCCount(k[pos])
		pos++
	}
	key := symKey(password, salt, k[2], k[3], d.count, symKeySize(k[1]))
	defer clear(key)
	d.profile.cipher = k[1]
	if pos < len(k) {
		d.profile.session = true
		wrap, e := symBlock(k[1], key)
		if e != nil {
			return d, e
		}
		session := symCFB(wrap, k[pos:], true, false)
		defer clear(session)
		if len(session) == 0 {
			return d, fmt.Errorf("session")
		}
		d.profile.cipher = session[0]
		if symKeySizeKnown(session[0]) == 0 {
			return d, fmt.Errorf("emitted session cipher")
		}
		if len(session) != symKeySize(session[0])+1 {
			return d, fmt.Errorf("session size")
		}
		key = append([]byte(nil), session[1:]...)
		defer clear(key)
	}
	d.keySize = len(key)
	block, err := symBlock(d.profile.cipher, key)
	if err != nil {
		return d, err
	}
	return symDecodeData(rest, block, d)
}

// Share strict complete-packet, prefix, MDC and literal validation between the
// ordinary header-driven decoder and explicit mixed-selector emission probes.
func symDecodeData(rest []byte, block cipher.Block, d symDecoded) (symDecoded, error) {
	data, tail, err := pgpRFCRead(rest, false)
	if err != nil {
		return d, err
	}
	if len(tail) != 0 || (data.tag != 9 && data.tag != 18) {
		return d, fmt.Errorf("data packet")
	}
	d.partial = data.partial
	d.profile.noMDC = data.tag == 9
	body := data.body
	if data.tag == 18 {
		if len(body) == 0 || body[0] != 1 {
			return d, fmt.Errorf("SEIPD version")
		}
		body = body[1:]
	}
	bs := block.BlockSize()
	if len(body) < bs+2 {
		return d, fmt.Errorf("short data")
	}
	plain := symCFB(block, body, true, d.profile.noMDC)
	defer clear(plain)
	if !bytes.Equal(plain[bs-2:bs], plain[bs:bs+2]) {
		return d, fmt.Errorf("prefix")
	}
	end := len(plain)
	if !d.profile.noMDC {
		if end < bs+2+22 {
			return d, fmt.Errorf("short MDC")
		}
		sum := sha1.Sum(plain[:end-20])
		if !bytes.Equal(sum[:], plain[end-20:]) || !bytes.Equal(plain[end-22:end-20], []byte{211, 20}) {
			return d, fmt.Errorf("MDC")
		}
		end -= 22
	}
	err = symInner(plain[bs+2:end], &d, false)
	return d, err
}

func symInner(inner []byte, d *symDecoded, nested bool) error {
	for len(inner) > 0 {
		p, tail, err := pgpRFCRead(inner, true)
		if err != nil {
			return err
		}
		switch p.tag {
		case 11:
			if len(p.body) < 6 {
				return fmt.Errorf("literal header")
			}
			offset := 6 + int(p.body[1])
			if offset > len(p.body) {
				return fmt.Errorf("literal name")
			}
			d.formats = append(d.formats, p.body[0])
			d.filenameLength += int(p.body[1])
			d.data = append(d.data, p.body[offset:]...)
		case 8:
			if nested || len(d.formats) != 0 || len(p.body) == 0 {
				return fmt.Errorf("compression grammar")
			}
			d.profile.compression = p.body[0]
			compressed := p.body[1:]
			var r io.ReadCloser
			switch p.body[0] {
			case 0:
				r = io.NopCloser(bytes.NewReader(compressed))
			case 1:
				r = flate.NewReader(bytes.NewReader(compressed))
			case 2:
				r, err = zlib.NewReader(bytes.NewReader(compressed))
			default:
				return fmt.Errorf("compression algorithm")
			}
			if err != nil {
				return err
			}
			decoded, e := io.ReadAll(r)
			closeErr := r.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			err = symInner(decoded, d, true)
			clear(decoded)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("inner packet %d", p.tag)
		}
		inner = tail
	}
	if len(d.formats) == 0 {
		return fmt.Errorf("missing literal")
	}
	return nil
}

func symAnchor(t *testing.T) {
	t.Helper()
	pgpRFCAnchor(t)
	d, err := symDecode(pgpVector(t), "foobar")
	if err != nil || string(d.data) != "Secret message." {
		t.Fatal("independent AES anchor", err)
	}
	key, _ := hex.DecodeString("0123456712345678234567893456789a")
	plain, _ := hex.DecodeString("0123456789abcdef")
	want, _ := hex.DecodeString("238b4fe5847e44b2")
	block, err := cast5.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 8)
	block.Encrypt(out, plain)
	if !bytes.Equal(out, want) {
		t.Fatal("RFC2144 Appendix B.1 CAST anchor")
	}
	block.Decrypt(out, out)
	if !bytes.Equal(out, plain) {
		t.Fatal("CAST reverse anchor")
	}
	// NIST FIPS197 Appendix C.1/C.2/C.3 anchors all three AES key sizes.
	for _, v := range []struct{ key, ciphertext string }{
		{"000102030405060708090a0b0c0d0e0f", "69c4e0d86a7b0430d8cdb78070b4c55a"},
		{"000102030405060708090a0b0c0d0e0f1011121314151617", "dda97ca4864cdfe06eaf70a0ec0d7191"},
		{"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "8ea2b7ca516745bfeafc49904b496089"},
	} {
		k, _ := hex.DecodeString(v.key)
		b, e := aes.NewCipher(k)
		if e != nil {
			t.Fatal(e)
		}
		input, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
		got := make([]byte, 16)
		b.Encrypt(got, input)
		if hex.EncodeToString(got) != v.ciphertext {
			t.Fatal("FIPS197 AES anchor")
		}
	}
	// Bruce Schneier's published Blowfish zero-key/zero-block vector.
	bf, err := blowfish.NewCipher(make([]byte, 8))
	if err != nil {
		t.Fatal(err)
	}
	bf.Encrypt(out, make([]byte, 8))
	if hex.EncodeToString(out) != "4ef997456198dd78" {
		t.Fatal("Blowfish anchor")
	}
	zip, err := symDecode(symBase64(t, symZIP64), "key")
	if err != nil || string(zip.data) != "Secret message" || zip.profile.compression != 1 || zip.profile.cipher != 9 {
		t.Fatal("official ZIP anchor", err)
	}
	nomdc, err := symDecode(symBase64(t, symNoMDC64), "foobar")
	if err != nil || string(nomdc.data) != "Secret message." || !nomdc.profile.noMDC {
		t.Fatal("official OpenPGP resync anchor", err)
	}
}

// Fixed REL_18_6 contrib/pgcrypto/sql/pgp-compression.sql and pgp-decrypt.sql.
const symZIP64 = "ww0ECQMCsci6AdHnELlh0kQB4jFcVwHMJg0Bulop7m3Mi36s15TAhBo0AnzIrRFrdLVCkKohsS6+DMcmR53SXfLoDJOv/M8uKj3QSq7oWNIp95pxfA=="
const symNoMDC64 = "jA0EBwMCnv07rlXqWctgyS2Dm2JfOKCRL4sLSLJUC8RS2cH7cIhKSuLitOtyquB+u9YkgfJfsuRJmgQ9tmo="
const symNUL64 = "jA0EBwMCXLc8pozB10Fg0jQBVUID59TLvWutJp0j6eh9ZgjqIRzdYaIymFB8y4XHvu0YlJP5D5BX7yqZ+Pry7TlDmiFO"

func symBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func symKeySizeKnown(id byte) int {
	switch id {
	case 2, 8:
		return 24
	case 3, 4, 7:
		return 16
	case 9:
		return 32
	default:
		return 0
	}
}
