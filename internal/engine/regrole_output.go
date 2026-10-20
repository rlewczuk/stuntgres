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
	owner    *catalog.Owner
	roles    authorization.Read
	quoteAll bool
}

func (r capturedRegroleOutput) FormatRegrole(id uint32) (string, error) {
	if id == 0 {
		return "-", nil
	}
	if r.owner == nil || !r.roles.BelongsTo(r.owner.Principals) {
		return "", fmt.Errorf("stuntgres: missing regrole output authority")
	}
	role, ok := r.roles.Find(r.owner.Principals.Principal(uint64(id)))
	if !ok {
		return strconv.FormatUint(uint64(id), 10), nil
	}
	if r.quoteAll {
		return catalog.QuoteIdentifier(role.Name), nil
	}
	return regclassIdentifier(role.Name), nil
}

// Native serialization already carries the immutable cluster role root for ACL
// output. Regrole uses that same root, but permits unknown numeric identities.
func regroleContextFormatter(ctx context.Context) sqltype.RegroleFormatter {
	if output, ok := ctx.Value(aclItemOutputKey{}).(capturedACLItemOutput); ok {
		return capturedRegroleOutput{owner: output.owner, roles: output.roles}
	}
	return nil
}

func (s *StatementScope) FormatRegrole(id uint32) (string, error) {
	if s == nil {
		return exec.DetachedPresentation{}.FormatRegrole(id)
	}
	return (capturedRegroleOutput{owner: s.inquiry.commentCatalog.Owner(), roles: s.inquiry.commentRoles, quoteAll: s.output.QuoteAllIdentifiers}).FormatRegrole(id)
}

func captureRegroleOutputValue(rows *exec.FinalRows, output capturedRegroleOutput, value sqltype.Value) error {
	if value.IsNull() || !value.Type().ContainsRegrole() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := captureRegroleOutputValue(rows, output, value.CompositeField(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := captureRegroleOutputValue(rows, output, value.ArrayElement(i)); err != nil {
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

// This is a dependency/persistence restriction, not a data codec restriction.
// Opaque array payloads, domains, runtime casts and typed NULL are not scalar
// REGROLE Const nodes. Their ordinary expression children are checked by callers.
func validateRegroleStoredLiteral(value sqltype.Value) error {
	if value.Type().ID() == sqltype.Regrole && !value.IsNull() {
		return sqlError("0A000", "constant of the type regrole cannot be used here")
	}
	return nil
}
