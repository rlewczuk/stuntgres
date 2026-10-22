package functions

import (
	"context"
	"crypto/md5"
	"crypto/sha1"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func uuidOsspName(name string) bool {
	return name == "uuid_generate_v3" || name == "uuid_generate_v5"
}

// uuidNameHash follows RFC9562 sections5.3/5.5. Namespace bytes already have
// network order; the name is hashed verbatim, without normalization or a NUL.
func uuidNameHash(ctx context.Context, namespace [16]byte, name string, version byte) ([16]byte, error) {
	if err := ctx.Err(); err != nil {
		return [16]byte{}, err
	}
	var scratch [4096]byte
	var digest [sha1.Size]byte
	// Keep each concrete standard-library hash at its call site so interface
	// dispatch does not force hash state and scratch buffers onto the heap.
	if version == 3 {
		h := md5.New()
		h.Write(namespace[:])
		for len(name) != 0 {
			if err := ctx.Err(); err != nil {
				return [16]byte{}, err
			}
			n := copy(scratch[:], name)
			h.Write(scratch[:n])
			name = name[n:]
		}
		h.Sum(digest[:0])
	} else {
		h := sha1.New()
		h.Write(namespace[:])
		for len(name) != 0 {
			if err := ctx.Err(); err != nil {
				return [16]byte{}, err
			}
			n := copy(scratch[:], name)
			h.Write(scratch[:n])
			name = name[n:]
		}
		h.Sum(digest[:0])
	}
	if err := ctx.Err(); err != nil {
		return [16]byte{}, err
	}
	var uuid [16]byte
	copy(uuid[:], digest[:16])
	uuid[6] = uuid[6]&0x0f | version<<4
	uuid[8] = uuid[8]&0x3f | 0x80
	return uuid, nil
}

func (e UUIDGeneration) evalName(ctx context.Context, c expr.Context) (sqltype.Value, error) {
	var values [2]sqltype.Value
	// Match the shared strict scalar contract: demanded operands are evaluated
	// before NULL eliminates the kernel, including errors in the other operand.
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	namespace, err := values[0].UUID()
	if err != nil {
		return sqltype.Value{}, err
	}
	name, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	version := byte(3)
	if e.Name == "uuid_generate_v5" {
		version = 5
	}
	uuid, err := uuidNameHash(ctx, namespace, name, version)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.UUIDValue(e.Usage.Type, uuid)
}
