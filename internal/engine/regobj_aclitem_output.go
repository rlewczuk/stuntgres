package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

type aclItemOutputKey struct{}

func ACLItemOutputContext(ctx context.Context, output capturedACLItemOutput) context.Context {
	return context.WithValue(ctx, aclItemOutputKey{}, output)
}

func ACLItemContextFormatter(ctx context.Context) sqltype.ACLItemFormatter {
	output, ok := ctx.Value(aclItemOutputKey{}).(capturedACLItemOutput)
	if !ok {
		return nil
	}
	return output
}

// Captured role roots are immutable; formatting never consults a live Engine.
type capturedACLItemOutput struct {
	Owner    *catalog.Owner
	Roles    authorization.Read
	Lossless bool
}

func (r capturedACLItemOutput) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if r.Owner == nil || authority != r.Owner.ACLAuthority || !r.Roles.BelongsTo(r.Owner.Principals) {
		return "", fmt.Errorf("stuntgres: foreign ACL identity")
	}
	if id == 0 {
		if r.Lossless {
			return "", fmt.Errorf("stuntgres: dump cannot serialize zero ACL grantor")
		}
		return "0", nil
	}
	role, ok := r.Roles.Find(r.Owner.Principals.Principal(uint64(id)))
	if !ok {
		if r.Lossless {
			return "", fmt.Errorf("stuntgres: dump cannot serialize stale ACL identity %d", id)
		}
		return strconv.FormatUint(uint64(id), 10), nil
	}
	return role.Name, nil
}

// Validation checks immutable datum provenance without a role-liveness lookup:
// dropped role IDs remain valid stored ACL data. Its text is discarded and is
// never used for SQL replay, Dump, deparse or result presentation.
type aclItemValidationOutput struct{ Owner *catalog.Owner }

func (r aclItemValidationOutput) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if r.Owner == nil || authority != r.Owner.ACLAuthority {
		return "", fmt.Errorf("stuntgres: foreign ACL validation identity")
	}
	return strconv.FormatUint(uint64(id), 10), nil
}

func CaptureACLItemOutputValue(rows *exec.FinalRows, output capturedACLItemOutput, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsACLItem() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureACLItemOutputValue(rows, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureACLItemOutputValue(rows, output, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	authority, grantee, grantor, _, _, err := value.ACLItem()
	if err != nil {
		return err
	}
	if output.Owner == nil || authority != output.Owner.ACLAuthority {
		return fmt.Errorf("stuntgres: foreign ACL result identity")
	}
	for _, id := range [2]uint32{grantee, grantor} {
		if _, ok := rows.AclitemOutput[id]; ok {
			continue
		}
		name, err := output.FormatACLRole(authority, id)
		if err != nil {
			return err
		}
		if rows.AclitemOutput == nil {
			rows.AclitemOutput = make(map[uint32]string)
			rows.ACLAuthority = authority
			rows.Output.ACLItem = rows
		}
		rows.AclitemOutput[id] = name
	}
	return nil
}
