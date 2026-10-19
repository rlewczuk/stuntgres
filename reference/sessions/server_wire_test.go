//go:build !noservertests

package reference_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type serverFrame struct {
	tag  byte
	body []byte
}
type serverWire struct {
	net.Conn
	pid      uint32
	key      []byte
	protocol uint32
	statuses map[string]string
}

func serverPut32(b []byte, n uint32) []byte { return binary.BigEndian.AppendUint32(b, n) }
func serverPacket(tag byte, body []byte) []byte {
	b := []byte{tag}
	b = serverPut32(b, uint32(len(body)+4))
	return append(b, body...)
}
func serverStartup(version uint32, pairs ...string) []byte {
	b := serverPut32(nil, version)
	for _, s := range pairs {
		b = append(b, []byte(s)...)
		b = append(b, 0)
	}
	b = append(b, 0)
	return append(serverPut32(nil, uint32(len(b)+4)), b...)
}
func serverEndpoint(t *testing.T, dsn string) (string, string, string, string) {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil || u.Host == "" {
		t.Fatalf("URL DSN required: %q: %v", dsn, e)
	}
	password, _ := u.User.Password()
	return u.Host, u.User.Username(), password, strings.TrimPrefix(u.Path, "/")
}
func serverDial(t *testing.T, dsn string) *serverWire {
	t.Helper()
	addr, _, _, _ := serverEndpoint(t, dsn)
	c, e := net.DialTimeout("tcp", addr, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	w := &serverWire{Conn: c, statuses: map[string]string{}}
	t.Cleanup(func() { c.Close() })
	if e = c.SetDeadline(time.Now().Add(20 * time.Second)); e != nil {
		t.Fatal(e)
	}
	return w
}
func (w *serverWire) send(t *testing.T, b []byte) {
	t.Helper()
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			t.Fatal(e)
		}
		b = b[n:]
	}
}
func (w *serverWire) readFrame() (serverFrame, error) {
	var h [5]byte
	if _, e := io.ReadFull(w, h[:]); e != nil {
		return serverFrame{}, e
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n < 4 || n > 32<<20 {
		return serverFrame{}, fmt.Errorf("invalid backend length %d", n)
	}
	b := make([]byte, n-4)
	if _, e := io.ReadFull(w, b); e != nil {
		return serverFrame{}, e
	}
	return serverFrame{h[0], b}, nil
}
func (w *serverWire) frame(t *testing.T) serverFrame {
	t.Helper()
	f, e := w.readFrame()
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func serverFields(body []byte) map[byte]string {
	out := map[byte]string{}
	for len(body) > 1 && body[0] != 0 {
		n := bytes.IndexByte(body[1:], 0)
		if n < 0 {
			break
		}
		out[body[0]] = string(body[1 : n+1])
		body = body[n+2:]
	}
	return out
}
func (w *serverWire) login(t *testing.T, dsn string, version uint32, extra ...string) []serverFrame {
	t.Helper()
	_, user, password, db := serverEndpoint(t, dsn)
	pairs := []string{"user", user, "database", db}
	pairs = append(pairs, extra...)
	w.protocol = version
	w.send(t, serverStartup(version, pairs...))
	return w.authenticate(t, user, password)
}
func (w *serverWire) authenticate(t *testing.T, user, password string) []serverFrame {
	t.Helper()
	if w.protocol == 0 {
		w.protocol = 196608 // Manual fragmented-startup witnesses use protocol3.0.
	}
	var frames []serverFrame
	var first, bare, nonce, serverFirst, expected string
	for {
		f := w.frame(t)
		frames = append(frames, f)
		switch f.tag {
		case 'v':
			if len(f.body) < 8 {
				t.Fatal("short protocol negotiation")
			}
			version := binary.BigEndian.Uint32(f.body)
			if version < 196608 || version > w.protocol || version>>16 != 3 {
				t.Fatalf("invalid negotiated protocol version %d", version)
			}
			w.protocol = version
		case 'R':
			if len(f.body) < 4 {
				t.Fatal("short authentication")
			}
			switch binary.BigEndian.Uint32(f.body) {
			case 0:
			case 3:
				w.send(t, serverPacket('p', append([]byte(password), 0)))
			case 5:
				if len(f.body) != 8 {
					t.Fatal("MD5 salt")
				}
				a := md5.Sum([]byte(password + user))
				b := md5.Sum(append([]byte(hex.EncodeToString(a[:])), f.body[4:]...))
				w.send(t, serverPacket('p', append([]byte("md5"+hex.EncodeToString(b[:])), 0)))
			case 10:
				nonce = "c2VydmVyLWNvbnRyYWN0LW5vbmNl"
				bare = "n=" + strings.ReplaceAll(strings.ReplaceAll(user, "=", "=3D"), ",", "=2C") + ",r=" + nonce
				first = "n,," + bare
				p := append([]byte("SCRAM-SHA-256"), 0)
				p = serverPut32(p, uint32(len(first)))
				w.send(t, serverPacket('p', append(p, first...)))
			case 11:
				serverFirst = string(f.body[4:])
				fields := map[string]string{}
				for _, v := range strings.Split(serverFirst, ",") {
					k, val, ok := strings.Cut(v, "=")
					if ok {
						fields[k] = val
					}
				}
				if !strings.HasPrefix(fields["r"], nonce) {
					t.Fatal("SCRAM nonce")
				}
				salt, e := base64.StdEncoding.DecodeString(fields["s"])
				if e != nil {
					t.Fatal(e)
				}
				iters, e := strconv.Atoi(fields["i"])
				if e != nil {
					t.Fatal(e)
				}
				salted, e := pbkdf2.Key(sha256.New, password, salt, iters, 32)
				if e != nil {
					t.Fatal(e)
				}
				mac := func(k []byte, s string) []byte { h := hmac.New(sha256.New, k); h.Write([]byte(s)); return h.Sum(nil) }
				client := mac(salted, "Client Key")
				stored := sha256.Sum256(client)
				final := "c=biws,r=" + fields["r"]
				auth := bare + "," + serverFirst + "," + final
				signature := mac(stored[:], auth)
				for i := range client {
					client[i] ^= signature[i]
				}
				expected = base64.StdEncoding.EncodeToString(mac(mac(salted, "Server Key"), auth))
				w.send(t, serverPacket('p', []byte(final+",p="+base64.StdEncoding.EncodeToString(client))))
			case 12:
				if string(f.body[4:]) != "v="+expected {
					t.Fatal("SCRAM server signature")
				}
			default:
				t.Fatalf("authentication method %d", binary.BigEndian.Uint32(f.body))
			}
		case 'S':
			v := bytes.Split(f.body, []byte{0})
			if len(v) >= 2 {
				w.statuses[string(v[0])] = string(v[1])
			}
		case 'K':
			if len(f.body) < 8 || len(f.body) > 260 || (w.protocol < 196610 && len(f.body) != 8) {
				t.Fatal("backend key length")
			}
			w.pid = binary.BigEndian.Uint32(f.body)
			w.key = bytes.Clone(f.body[4:])
		case 'E':
			return frames
		case 'Z':
			if string(f.body) != "I" {
				t.Fatalf("startup ready %q", f.body)
			}
			return frames
		}
	}
}
func (w *serverWire) query(t *testing.T, q string) []serverFrame {
	t.Helper()
	w.send(t, serverPacket('Q', append([]byte(q), 0)))
	return w.ready(t)
}
func (w *serverWire) ready(t *testing.T) []serverFrame {
	t.Helper()
	var out []serverFrame
	for {
		f := w.frame(t)
		out = append(out, f)
		if f.tag == 'Z' {
			return out
		}
	}
}
func serverCode(frames []serverFrame) string {
	for _, f := range frames {
		if f.tag == 'E' {
			return serverFields(f.body)['C']
		}
	}
	return ""
}
func serverWantCode(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	if got := serverCode(frames); got != want {
		t.Fatalf("SQLSTATE %q want %q: %v", got, want, frames)
	}
}
func serverTags(frames []serverFrame) string {
	var b []byte
	for _, f := range frames {
		b = append(b, f.tag)
	}
	return string(b)
}
func serverValues(t *testing.T, frames []serverFrame) [][]string {
	t.Helper()
	var out [][]string
	for _, f := range frames {
		if f.tag != 'D' {
			continue
		}
		b := f.body
		if len(b) < 2 {
			t.Fatal("short row")
		}
		n := int(binary.BigEndian.Uint16(b))
		b = b[2:]
		row := make([]string, n)
		for i := range row {
			if len(b) < 4 {
				t.Fatal("short value")
			}
			sz := int32(binary.BigEndian.Uint32(b))
			b = b[4:]
			if sz == -1 {
				row[i] = "<NULL>"
				continue
			}
			if sz < 0 || int(sz) > len(b) {
				t.Fatal("value length")
			}
			row[i] = string(b[:sz])
			b = b[sz:]
		}
		if len(b) != 0 {
			t.Fatal("row trailer")
		}
		out = append(out, row)
	}
	return out
}
func serverScalar(t *testing.T, w *serverWire, q, want string) {
	t.Helper()
	f := w.query(t, q)
	serverWantCode(t, f, "")
	v := serverValues(t, f)
	if len(v) != 1 || len(v[0]) != 1 || v[0][0] != want {
		t.Fatalf("%s: values %v want %q", q, v, want)
	}
}
func serverCancel(t *testing.T, dsn string, pid uint32, key []byte) {
	t.Helper()
	addr, _, _, _ := serverEndpoint(t, dsn)
	c, e := net.DialTimeout("tcp", addr, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	b := serverPut32(nil, uint32(12+len(key)))
	b = serverPut32(b, 80877102)
	b = serverPut32(b, pid)
	b = append(b, key...)
	if _, e = c.Write(b); e != nil {
		t.Fatal(e)
	}
	// EOF acknowledges handling of the cancellation connection, not delivery
	// or observation by the target executor. CancelRequest has no response payload.
	if e = c.SetReadDeadline(time.Now().Add(5 * time.Second)); e != nil {
		t.Fatal(e)
	}
	var reply [1]byte
	if _, e = c.Read(reply[:]); e != io.EOF {
		t.Fatalf("CancelRequest completion: %v", e)
	}
}
func serverFrameSummary(frames []serverFrame) string {
	return fmt.Sprintf("tags=%s code=%s", serverTags(frames), serverCode(frames))
}
