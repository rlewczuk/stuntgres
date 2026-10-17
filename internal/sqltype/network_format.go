package sqltype

import (
	"context"
	"fmt"
	"strconv"
)

// NetworkFormatKind identifies only the selected network scalar signatures.
// TEXT(INET) remains the existing explicit coercion, not a second provider.
type NetworkFormatKind uint8

const (
	NetworkAbbrev NetworkFormatKind = iota + 1
	NetworkHost
	NetworkFamily
	NetworkMasklen
	NetworkSameFamily
	NetworkBroadcast
)

func (k NetworkFormatKind) Name() string {
	switch k {
	case NetworkAbbrev:
		return "abbrev"
	case NetworkHost:
		return "host"
	case NetworkFamily:
		return "family"
	case NetworkMasklen:
		return "masklen"
	case NetworkSameFamily:
		return "inet_same_family"
	case NetworkBroadcast:
		return "broadcast"
	}
	return ""
}
func (k NetworkFormatKind) Result() ID {
	switch k {
	case NetworkAbbrev, NetworkHost:
		return Text
	case NetworkFamily, NetworkMasklen:
		return Int4
	case NetworkSameFamily:
		return Bool
	case NetworkBroadcast:
		return Inet
	}
	return 0
}
func (k NetworkFormatKind) Arity() int {
	if k == NetworkSameFamily {
		return 2
	}
	if k.Name() != "" {
		return 1
	}
	return 0
}
func NetworkFormatFunction(name string) NetworkFormatKind {
	switch name {
	case "abbrev":
		return NetworkAbbrev
	case "host":
		return NetworkHost
	case "family":
		return NetworkFamily
	case "masklen":
		return NetworkMasklen
	case "inet_same_family":
		return NetworkSameFamily
	case "broadcast":
		return NetworkBroadcast
	}
	return 0
}

// NetworkFormat owns exact descriptor pointers, never a catalog or permission
// snapshot. CIDR promotion to INET is performed by the ordinary binder.
type NetworkFormat struct {
	kind                NetworkFormatKind
	result, left, right *Descriptor
}

func BindNetworkFormat(kind NetworkFormatKind, result, left, right *Descriptor) (NetworkFormat, error) {
	valid := kind.Arity() != 0 && result != nil && result.id == kind.Result() && left != nil && left.id == Inet
	if kind == NetworkAbbrev {
		valid = result != nil && result.id == Text && left != nil && IsNetwork(left.id)
	}
	if kind == NetworkSameFamily {
		valid = valid && right == left
	} else {
		valid = valid && right == nil
	}
	if kind == NetworkBroadcast {
		valid = valid && result == left
	}
	if !valid {
		return NetworkFormat{}, fmt.Errorf("invalid network formatting signature")
	}
	return NetworkFormat{kind, result, left, right}, nil
}
func (p NetworkFormat) Unary(ctx context.Context, a Value) (Value, error) {
	if p.kind.Arity() != 1 || p.result == nil || p.left == nil || p.right != nil || a.typ != p.left {
		return Value{}, fmt.Errorf("invalid unary network formatting values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.result), nil
	}
	if !a.network.valid(p.left.id == Cidr) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	n := a.network
	switch p.kind {
	case NetworkAbbrev:
		if p.left.id == Cidr {
			return String(p.result, n.abbreviateCIDR()), nil
		}
		return String(p.result, n.output(false)), nil
	case NetworkHost:
		n.prefix = uint8(networkWidth(n) * 8)
		return String(p.result, n.output(false)), nil
	case NetworkFamily:
		return Integer(p.result, int64(n.family))
	case NetworkMasklen:
		return Integer(p.result, int64(n.prefix))
	case NetworkBroadcast:
		width := networkWidth(n)
		for i := int(n.prefix) / 8; i < width; i++ {
			bits := int(n.prefix) - i*8
			if bits > 0 {
				n.address[i] |= byte(0xff >> bits)
			} else {
				n.address[i] = 0xff
			}
		}
		return Value{typ: p.result, network: n}, nil
	}
	return Value{}, fmt.Errorf("invalid network formatting operation")
}
func (p NetworkFormat) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.kind != NetworkSameFamily || p.result == nil || p.left == nil || p.right != p.left || a.typ != p.left || b.typ != p.right {
		return Value{}, fmt.Errorf("invalid binary network formatting values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	if !a.network.valid(false) || !b.network.valid(false) {
		return Value{}, fmt.Errorf("invalid network datum")
	}
	return Boolean(p.result, a.network.family == b.network.family), nil
}

// This bounded translation preserves REL_18_6 inet_cidr_ntop's truncated-word,
// single-zero and dotted-tail rules, including its unusual temporary zero-run
// bookkeeping. It is not ordinary INET/RFC5952 output with suffixes removed.
// Copyright (c) 2004 Internet Systems Consortium, Inc. (ISC)
// Copyright (c) 1996,1999 Internet Software Consortium.
// Permission to use, copy, modify, and distribute this software for any purpose
// with or without fee is hereby granted, provided that the above copyright
// notice and this permission notice appear in all copies.
// THE SOFTWARE IS PROVIDED "AS IS" AND ISC DISCLAIMS ALL WARRANTIES WITH REGARD
// TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
// FITNESS. IN NO EVENT SHALL ISC BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT,
// OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF
// USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
// TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
// OF THIS SOFTWARE.
func (n networkDatum) abbreviateCIDR() string {
	var buffer [64]byte
	dst := buffer[:0]
	if n.family == 4 {
		if n.prefix == 0 {
			dst = append(dst, '0')
		} else {
			dst = appendNetworkIPv4(dst, n.address[:(int(n.prefix)+7)/8])
		}
	} else if n.prefix == 0 {
		dst = append(dst, ':', ':')
	} else {
		words := (int(n.prefix) + 15) / 16
		if words == 1 {
			words = 2
		}
		zeroStart, zeroLength, tmpStart, tmpLength := 0, 0, 0, 0
		for i := range words {
			if n.address[2*i]|n.address[2*i+1] == 0 {
				if tmpLength == 0 {
					tmpStart = i
				}
				tmpLength++
			} else if tmpLength != 0 && zeroLength < tmpLength {
				zeroStart, zeroLength, tmpLength = tmpStart, tmpLength, 0
			}
		}
		if tmpLength != 0 && zeroLength < tmpLength {
			zeroStart, zeroLength = tmpStart, tmpLength
		}
		dotted := zeroLength != words && zeroStart == 0 && (zeroLength == 6 || zeroLength == 5 && n.address[10] == 0xff && n.address[11] == 0xff || zeroLength == 7 && n.address[14] != 0 && n.address[15] != 1)
		offset := 0
		for p := range words {
			if zeroLength != 0 && p >= zeroStart && p < zeroStart+zeroLength {
				if p == zeroStart {
					dst = append(dst, ':')
				}
				if p == words-1 {
					dst = append(dst, ':')
				}
				offset += 2
				continue
			}
			if dotted && p > 5 {
				sep := byte('.')
				if p == 6 {
					sep = ':'
				}
				dst = append(dst, sep)
				dst = strconv.AppendUint(dst, uint64(n.address[offset]), 10)
				offset++
				if p != 7 || n.prefix > 120 {
					dst = append(dst, '.')
					dst = strconv.AppendUint(dst, uint64(n.address[offset]), 10)
					offset++
				}
			} else {
				if len(dst) > 0 {
					dst = append(dst, ':')
				}
				dst = strconv.AppendUint(dst, uint64(n.address[offset])<<8|uint64(n.address[offset+1]), 16)
				offset += 2
			}
		}
	}
	dst = append(dst, '/')
	dst = strconv.AppendUint(dst, uint64(n.prefix), 10)
	return string(dst)
}
