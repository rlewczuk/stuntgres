package main

import (
	"encoding/binary"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// protocolInput only consumes bounded intact frame storage. Semantic ordering
// belongs to the engine, particularly Bind and fastpath receive/conversion.
type protocolInput struct{ body []byte }

func protocolError(message string) error { return &sqlstate.Error{Code: "08P01", Message: message} }
func (p *protocolInput) Uint16() (uint16, error) {
	b, err := p.Bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b), nil
}
func (p *protocolInput) Int32() (int32, error) {
	b, err := p.Bytes(4)
	if err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b)), nil
}
func (p *protocolInput) Bytes(n int) ([]byte, error) {
	if n < 0 || n > len(p.body) {
		return nil, protocolError("insufficient data left in message")
	}
	b := p.body[:n]
	p.body = p.body[n:]
	return b, nil
}
func (p *protocolInput) Byte() (byte, error) {
	if len(p.body) == 0 {
		return 0, protocolError("no data left in message")
	}
	b := p.body[0]
	p.body = p.body[1:]
	return b, nil
}
func (p *protocolInput) String() (string, error) {
	value, err := consumeString(&p.body)
	if err == nil {
		err = sqltype.ValidateTextInput(value)
	}
	return value, err
}
func (p *protocolInput) End() error {
	if len(p.body) != 0 {
		return protocolError("invalid message format")
	}
	return nil
}
