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

type capturedRegroleOutput struct {
	Owner    *catalog.Owner
	Roles    authorization.Read
	QuoteAll bool
}

func (r capturedRegroleOutput) FormatRegrole(id uint32) (string, error) {
	if id == 0 {
		return "-", nil
	}
	if r.Owner == nil || !r.Roles.BelongsTo(r.Owner.Principals) {
		return "", fmt.Errorf("stuntgres: missing regrole output authority")
	}
	role, ok := r.Roles.Find(r.Owner.Principals.Principal(uint64(id)))
	if !ok {
		return strconv.FormatUint(uint64(id), 10), nil
	}
	if r.QuoteAll {
		return catalog.QuoteIdentifier(role.Name), nil
	}
	return regclassIdentifier(role.Name), nil
}

// Native serialization already carries the immutable cluster role root for ACL
// output. Regrole uses that same root, but permits unknown numeric identities.
func RoleContextFormatter(ctx context.Context) sqltype.RegroleFormatter {
	if output, ok := ctx.Value(aclItemOutputKey{}).(capturedACLItemOutput); ok {
		return capturedRegroleOutput{Owner: output.Owner, Roles: output.Roles}
	}
	return nil
}

func CaptureRoleOutputValue(rows *exec.FinalRows, output capturedRegroleOutput, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegrole() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := CaptureRoleOutputValue(rows, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := CaptureRoleOutputValue(rows, output, value.ArrayElement(i)); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.RoleOID()
	if err != nil {
		return err
	}
	if _, ok := rows.RegroleOutput[id]; ok {
		return nil
	}
	text, err := output.FormatRegrole(id)
	if err != nil {
		return err
	}
	if rows.RegroleOutput == nil {
		rows.RegroleOutput = make(map[uint32]string)
	}
	rows.RegroleOutput[id] = text
	rows.Output.Regrole = rows
	return nil
}
