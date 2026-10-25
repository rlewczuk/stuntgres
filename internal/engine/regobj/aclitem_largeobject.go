package regobj

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// LargeObjectACL decodes the ACL text of a Restore large-object ACL repair.
// The text must be an aclitem[] of this cluster that input reads back to the
// same canonical spelling under output, and its items may grant only SELECT
// and UPDATE. Grantees and grantors map to the principals of output's owner.
func LargeObjectACL(ctx context.Context, view catalog.Read, text string, input ACLItemInput, output ACLItemOutput) ([]authorization.ACLEntry, error) {
	return ObjectACL(ctx, view, text, input, output, authorization.SelectPrivilege|authorization.UpdatePrivilege)
}

// ObjectACL decodes canonical raw ACLs into the destination role authority.
func ObjectACL(ctx context.Context, view catalog.Read, text string, input ACLItemInput, output ACLItemOutput, mask authorization.Privileges) ([]authorization.ACLEntry, error) {
	array, ok := view.LookupType(sqltype.ACLItemArray)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing ACL array type")
	}
	value, err := array.InputWithEnvironment(ctx, text, -1, sqltype.InputEnvironment{ACLItem: input})
	if err != nil {
		return nil, err
	}
	canonical, err := array.OutputWithEnvironment(ctx, value, sqltype.OutputEnvironment{ACLItem: output})
	if err != nil {
		return nil, err
	}
	if canonical != text {
		return nil, sqlError("0A000", "restore accepts only canonical object ACL arrays")
	}
	allowed, err := authorization.ACLItemBits(mask)
	if err != nil {
		return nil, err
	}
	privileges := func(bits uint32) authorization.Privileges {
		var out authorization.Privileges
		for p := authorization.Privileges(1); p <= authorization.AlterSystemPrivilege; p <<= 1 {
			if mask&p == 0 {
				continue
			}
			b, _ := authorization.ACLItemBits(p)
			if bits&b != 0 {
				out |= p
			}
		}
		return out
	}
	entries := make([]authorization.ACLEntry, value.ArrayLen())
	for i := range value.ArrayLen() {
		authority, grantee, grantor, rights, options, err := value.ArrayElement(i).ACLItem()
		if err != nil {
			return nil, err
		}
		if authority != output.Owner.ACLAuthority || (rights|options) & ^allowed != 0 || options & ^rights != 0 {
			return nil, fmt.Errorf("stuntgres: invalid object ACL privileges")
		}
		entry := authorization.ACLEntry{Grantor: output.Owner.Principals.Principal(uint64(grantor)), Privileges: privileges(rights), GrantOptions: privileges(options)}
		entry.Grantee.Public = grantee == 0
		if grantee != 0 {
			entry.Grantee.Role = output.Owner.Principals.Principal(uint64(grantee))
		}
		entries[i] = entry
	}
	return entries, nil
}
