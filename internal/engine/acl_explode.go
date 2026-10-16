package engine

import (
	"context"
	"math/bits"

	"stuntgres/internal/sqltype"
)

// Each invocation owns only its input and progress, not a materialized rowset.
type aclExplodeCursor struct {
	array                     sqltype.Value
	index                     int
	remaining                 uint32
	grantor, grantee, options uint32
	closed                    bool
}

func (n aclValueExpression) openACL(c *EvalContext) (aclExplodeCursor, error) {
	values, null, err := n.values(c)
	if err != nil {
		return aclExplodeCursor{}, err
	}
	if null {
		return aclExplodeCursor{closed: true}, nil
	}
	if err := checkACLValue(c, values[0]); err != nil {
		return aclExplodeCursor{}, err
	}
	return aclExplodeCursor{array: values[0]}, nil
}
func (s *aclExplodeCursor) close() { *s = aclExplodeCursor{closed: true} }
func (s *aclExplodeCursor) next(ctx context.Context, shape *sqltype.Descriptor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		s.close()
		return sqltype.Value{}, false, err
	}
	for !s.closed && s.remaining == 0 {
		if s.index&aclScanCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				s.close()
				return sqltype.Value{}, false, err
			}
		}
		if s.index == s.array.ArrayLen() {
			err := ctx.Err()
			s.close()
			if err != nil {
				return sqltype.Value{}, false, err
			}
			break
		}
		_, g, r, p, o, err := s.array.ArrayElement(s.index).ACLItem()
		if err != nil {
			s.close()
			return sqltype.Value{}, false, err
		}
		s.index++
		s.grantee = g
		s.grantor = r
		s.remaining = p
		s.options = o
	}
	if s.closed {
		return sqltype.Null(shape), false, nil
	}
	bit := bits.TrailingZeros32(s.remaining)
	s.remaining &^= 1 << bit
	fields := make([]sqltype.Value, 4)
	var err error
	fields[0], err = sqltype.OIDValue(shape.CompositeMember(0).Usage.Type, s.grantor)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[1], err = sqltype.OIDValue(shape.CompositeMember(1).Usage.Type, s.grantee)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	fields[2] = sqltype.String(shape.CompositeMember(2).Usage.Type, aclPrivilegeLabels[bit])
	fields[3] = sqltype.Boolean(shape.CompositeMember(3).Usage.Type, s.options&(1<<bit) != 0)
	value, err := sqltype.OwnedCompositeValue(shape, fields)
	return value, err == nil, err
}
