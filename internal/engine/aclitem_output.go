package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type aclItemOutputKey struct{}

func aclItemOutputContext(ctx context.Context, output capturedACLItemOutput) context.Context {
	return context.WithValue(ctx, aclItemOutputKey{}, output)
}

func aclItemContextFormatter(ctx context.Context) sqltype.ACLItemFormatter {
	output, ok := ctx.Value(aclItemOutputKey{}).(capturedACLItemOutput)
	if !ok {
		return nil
	}
	return output
}

// Captured role roots are immutable; formatting never consults a live Engine.
type capturedACLItemOutput struct {
	owner    *catalog.Owner
	roles    authorization.Read
	lossless bool
}

func (r capturedACLItemOutput) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if r.owner == nil || authority != r.owner.ACLAuthority || !r.roles.BelongsTo(r.owner.Principals) {
		return "", fmt.Errorf("stuntgres: foreign ACL identity")
	}
	if id == 0 {
		if r.lossless {
			return "", fmt.Errorf("stuntgres: dump cannot serialize zero ACL grantor")
		}
		return "0", nil
	}
	role, ok := r.roles.Find(r.owner.Principals.Principal(uint64(id)))
	if !ok {
		if r.lossless {
			return "", fmt.Errorf("stuntgres: dump cannot serialize stale ACL identity %d", id)
		}
		return strconv.FormatUint(uint64(id), 10), nil
	}
	return role.Name, nil
}

// Validation checks immutable datum provenance without a role-liveness lookup:
// dropped role IDs remain valid stored ACL data. Its text is discarded and is
// never used for SQL replay, Dump, deparse or result presentation.
type aclItemValidationOutput struct{ owner *catalog.Owner }

func (r aclItemValidationOutput) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if r.owner == nil || authority != r.owner.ACLAuthority {
		return "", fmt.Errorf("stuntgres: foreign ACL validation identity")
	}
	return strconv.FormatUint(uint64(id), 10), nil
}

func (s *StatementScope) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if s == nil || s.inquiry.commentCatalog.Owner() == nil {
		return detachedPresentation{}.FormatACLRole(authority, id)
	}
	return (capturedACLItemOutput{owner: s.inquiry.commentCatalog.Owner(), roles: s.inquiry.commentRoles}).FormatACLRole(authority, id)
}

func captureACLItemOutputValue(rows *finalRows, output capturedACLItemOutput, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsACLItem() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureACLItemOutputValue(rows, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureACLItemOutputValue(rows, output, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	authority, grantee, grantor, _, _, err := value.ACLItem()
	if err != nil {
		return err
	}
	if output.owner == nil || authority != output.owner.ACLAuthority {
		return fmt.Errorf("stuntgres: foreign ACL result identity")
	}
	for _, id := range [2]uint32{grantee, grantor} {
		if _, ok := rows.aclitemOutput[id]; ok {
			continue
		}
		name, err := output.FormatACLRole(authority, id)
		if err != nil {
			return err
		}
		if rows.aclitemOutput == nil {
			rows.aclitemOutput = make(map[uint32]string)
			rows.aclAuthority = authority
			rows.output.ACLItem = rows
		}
		rows.aclitemOutput[id] = name
	}
	return nil
}
