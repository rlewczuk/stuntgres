package sqltype

import (
	"context"
	"io"
	"unsafe"

	"stuntgres/internal/sqltype/pgencoding18"
)

// EncodingReader converts before record framing. It owns bounded input/output
// buffers and at most a character/lookahead tail, never transport-owned bytes.
// A terminal source/conversion error is returned only after its converted prefix
// was consumed, so a recognized text end marker may leave that tail unread.
type EncodingReader struct {
	source                       io.Reader
	conversion                   EncodingConversion
	context                      func() context.Context
	input                        [32768]byte
	output                       [131080]byte
	begin, end, outBegin, outEnd int
	terminal, pending            error
}

func NewEncodingReader(source io.Reader, conversion EncodingConversion, currentContext func() context.Context) *EncodingReader {
	return &EncodingReader{source: source, conversion: conversion, context: currentContext}
}
func (r *EncodingReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if err := r.context().Err(); err != nil {
		return 0, err
	}
	for r.outBegin == r.outEnd {
		if r.pending != nil {
			return 0, r.pending
		}
		r.outBegin, r.outEnd = 0, 0
		validation := r.conversion.source
		if validation == 0 {
			validation = r.conversion.destination
		}
		nextPoll := r.begin
		for {
			if r.begin >= nextPoll {
				if err := r.context().Err(); err != nil {
					return 0, err
				}
				nextPoll = r.begin + 4096
			}
			remaining := r.end - r.begin
			if remaining == 0 || r.terminal == nil && (remaining < pgencoding18.Length(validation, r.view()) || r.conversion.source == 6 && r.conversion.destination != 6 && r.conversion.destination != 0 && remaining < 8) {
				if r.terminal != nil {
					r.pending = r.terminal
					break
				}
				if r.outEnd > 0 {
					break
				}
				copy(r.input[:], r.input[r.begin:r.end])
				r.begin, r.end, nextPoll = 0, remaining, 0
				n, err := r.source.Read(r.input[r.end:])
				r.end += n
				r.terminal = err
				if n == 0 && err == nil {
					if r.outEnd > 0 {
						break
					}
					continue
				}
				continue
			}
			raw := r.view()
			n := pgencoding18.Verify(validation, raw)
			if n < 0 {
				r.pending = binaryEncodingInvalid(raw, validation)
				break
			}
			if r.conversion.source == r.conversion.destination || r.conversion.source == 0 || r.conversion.destination == 0 {
				r.outEnd += copy(r.output[r.outEnd:], raw[:n])
				r.begin += n
			} else {
				if !r.conversion.Available() {
					r.pending = MissingBinaryConversion(r.conversion.source, r.conversion.destination)
					break
				}
				a, b, consumed, invalid := pgencoding18.Step(r.conversion.source, r.conversion.destination, raw)
				if invalid {
					r.pending = binaryEncodingInvalid(raw, r.conversion.source)
					break
				}
				if consumed == 0 {
					_, r.pending = binaryEncodingPass(r.context(), raw[:n], r.conversion.source, r.conversion.destination, nil)
					break
				}
				for _, word := range [2]uint32{a, b} {
					for shift := 24; shift >= 0; shift -= 8 {
						if v := byte(word >> uint(shift)); v != 0 {
							r.output[r.outEnd] = v
							r.outEnd++
						}
					}
				}
				r.begin += consumed
			}
			if r.outEnd > len(r.output)-8 {
				break
			}
		}
		if r.outEnd == 0 && r.pending != nil {
			return 0, r.pending
		}
	}
	n := copy(dst, r.output[r.outBegin:r.outEnd])
	r.outBegin += n
	return n, nil
}

// The transient string aliases only our own mutable input while kernels read it.
// It never escapes a Read iteration: conversion diagnostics format owned bytes,
// and output is copied into the separate owned output buffer before refill.
func (r *EncodingReader) view() string {
	b := r.input[r.begin:r.end]
	return unsafe.String(unsafe.SliceData(b), len(b))
}
