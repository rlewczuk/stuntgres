package fixture

import (
	"encoding/binary"
	"net"
)

// This observer reads only backend frame headers and Authentication codes. It
// forwards every byte untouched and never copies a password, salt, challenge,
// SASL payload or protocol trace. pgconn still owns the complete handshake.
type externalSQLAuthConn struct {
	net.Conn
	observation *externalSQLAuthObservation
}

func (c *externalSQLAuthConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.observation.consume(p[:n])
	return n, err
}

type externalSQLAuthObservation struct {
	header    [5]byte
	headerN   int
	code      [4]byte
	codeN     int
	remaining uint32
	candidate string
	saslFinal bool
	done      bool
	result    string
}

func (o *externalSQLAuthObservation) mechanism() string {
	if !o.done || o.result == "" {
		return "unknown"
	}
	return o.result
}

func (o *externalSQLAuthObservation) consume(p []byte) {
	for len(p) > 0 && !o.done {
		if o.headerN < len(o.header) {
			n := copy(o.header[o.headerN:], p)
			o.headerN += n
			p = p[n:]
			if o.headerN < len(o.header) {
				return
			}
			length := binary.BigEndian.Uint32(o.header[1:])
			if length < 4 || length > 1<<24 {
				o.done = true
				return
			}
			o.remaining = length - 4
			o.codeN = 0
			if o.header[0] == 'R' && o.remaining < 4 {
				o.done = true
				return
			}
		}
		n := len(p)
		if uint32(n) > o.remaining {
			n = int(o.remaining)
		}
		if o.header[0] == 'R' && o.codeN < len(o.code) {
			o.codeN += copy(o.code[o.codeN:], p[:n])
		}
		p = p[n:]
		o.remaining -= uint32(n)
		if o.remaining != 0 {
			continue
		}
		if o.header[0] == 'E' {
			o.done = true
			return
		}
		if o.header[0] == 'R' {
			switch binary.BigEndian.Uint32(o.code[:]) {
			case 0:
				o.done = true
				switch o.candidate {
				case "":
					o.result = "none"
				case "password", "md5":
					o.result = o.candidate
				case "scram-sha-256":
					if o.saslFinal {
						o.result = o.candidate
					}
				}
			case 3:
				o.candidate = "password"
			case 5:
				o.candidate = "md5"
			case 10:
				o.candidate = "scram-sha-256"
			case 11:
				if o.candidate != "scram-sha-256" {
					o.candidate = "unknown"
				}
			case 12:
				o.saslFinal = o.candidate == "scram-sha-256"
			default:
				o.candidate = "unknown"
			}
		}
		o.headerN = 0
	}
}
