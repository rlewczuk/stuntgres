package functions

import (
	"context"
	"fmt"
	"math/bits"
	"strings"

	"stuntgres/internal/sqltype"
)

// ACL item algorithms over raw aclitem bits, shared by the ACL SQL functions.
// This package cannot import authorization, so the privilege names and the
// default masks are kept here in aclitem bit order.

// aclPrivilegeLabels are the privilege names of the aclitem bits, in bit
// order.
var aclPrivilegeLabels = [...]string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "EXECUTE", "USAGE", "CREATE", "TEMPORARY", "CONNECT", "SET", "ALTER SYSTEM", "MAINTAIN"}

// aclPrivilegeNames parses makeaclitem's comma-separated privilege list.
func aclPrivilegeNames(text string) (uint32, error) {
	var mask uint32
	for part := range strings.SplitSeq(text, ",") {
		part = strings.Trim(part, " \t\n\r\v\f")
		found := false
		for i, label := range aclPrivilegeLabels {
			if aclPrivilegeEqual(part, label) || i == 10 && aclPrivilegeEqual(part, "TEMP") {
				mask |= 1 << i
				found = true
				break
			}
		}
		if !found {
			return 0, sqlError("22023", `unrecognized privilege type: "`+part+`"`)
		}
	}
	return mask, nil
}

// aclPrivilegeEqual compares ASCII case-insensitively, as PostgreSQL's
// pg_strcasecmp does; non-ASCII bytes never fold.
func aclPrivilegeEqual(input, upper string) bool {
	if len(input) != len(upper) {
		return false
	}
	for i := range len(input) {
		c := input[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != upper[i] {
			return false
		}
	}
	return true
}

// aclDefaultMasks are acldefault's owner and PUBLIC privileges for an object
// type abbreviation.
func aclDefaultMasks(code string) (uint32, uint32, error) {
	switch code {
	case "c":
		return 0, 0, nil
	case "r":
		return 16511, 0, nil
	case "s":
		return 262, 0, nil
	case "d":
		return 3584, 3072, nil
	case "f":
		return 128, 128, nil
	case "l":
		return 256, 256, nil
	case "L":
		return 6, 0, nil
	case "n":
		return 768, 0, nil
	case "p":
		return 12288, 0, nil
	case "t":
		return 512, 0, nil
	case "F", "S":
		return 256, 0, nil
	case "T":
		return 256, 256, nil
	}
	return 0, 0, sqlError("XX000", "unrecognized object type abbreviation: "+code)
}

// Bound cancellation latency without adding a context dispatch per ACL item.
const aclScanCheckMask = 255

// checkACLValue validates an aclitem[] argument: one dimension, no NULL
// items, and every item of the statement's own ACL authority.
func checkACLValue(ctx context.Context, source ACLAuthoritySource, array sqltype.Value) error {
	if array.ArrayRank() != 1 {
		return sqlError("22023", "ACL arrays must be one-dimensional")
	}
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		v := array.ArrayElement(i)
		if v.IsNull() {
			return sqlError("22004", "ACL arrays must not contain null values")
		}
	}
	authority := source.ACLAuthority()
	if authority == nil {
		return fmt.Errorf("stuntgres: ACL operation lacks statement authority")
	}
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		a, _, _, _, _, err := array.ArrayElement(i).ACLItem()
		if err != nil {
			return err
		}
		if a != authority {
			return fmt.Errorf("stuntgres: foreign ACL identity")
		}
	}
	return ctx.Err()
}

// aclContains is aclitem[] @> aclitem: some item has the grantee and
// grantor and covers the privileges and grant options.
func aclContains(ctx context.Context, source ACLAuthoritySource, array, item sqltype.Value) (bool, error) {
	if err := checkACLValue(ctx, source, array); err != nil {
		return false, err
	}
	a, g, r, p, o, err := item.ACLItem()
	if err != nil {
		return false, err
	}
	if a != source.ACLAuthority() {
		return false, fmt.Errorf("stuntgres: foreign ACL identity")
	}
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		aa, gg, rr, pp, oo, err := array.ArrayElement(i).ACLItem()
		if err != nil {
			return false, err
		}
		if a == aa && g == gg && r == rr && pp&p == p && oo&o == o {
			return true, nil
		}
	}
	return false, ctx.Err()
}

// ACLExplodeCursor is one aclexplode invocation. It owns only its input and
// progress, not a materialized rowset; the engine's set-returning function
// scan drives it.
type ACLExplodeCursor struct {
	array                     sqltype.Value
	index                     int
	remaining                 uint32
	grantor, grantee, options uint32
	closed                    bool
}

// Close releases the input; later Next calls report no row.
func (s *ACLExplodeCursor) Close() { *s = ACLExplodeCursor{closed: true} }

// Next returns the next (grantor, grantee, privilege_type, is_grantable)
// record of shape, one per granted privilege bit, or false at the end.
func (s *ACLExplodeCursor) Next(ctx context.Context, shape *sqltype.Descriptor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		s.Close()
		return sqltype.Value{}, false, err
	}
	for !s.closed && s.remaining == 0 {
		if s.index&aclScanCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				s.Close()
				return sqltype.Value{}, false, err
			}
		}
		if s.index == s.array.ArrayLen() {
			err := ctx.Err()
			s.Close()
			if err != nil {
				return sqltype.Value{}, false, err
			}
			break
		}
		_, g, r, p, o, err := s.array.ArrayElement(s.index).ACLItem()
		if err != nil {
			s.Close()
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
