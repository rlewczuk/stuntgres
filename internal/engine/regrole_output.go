package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

type capturedRegroleOutput struct {
	owner    *Engine
	roles    authorization.Read
	quoteAll bool
}

func (r capturedRegroleOutput) FormatRegrole(id uint32) (string, error) {
	if id == 0 {
		return "-", nil
	}
	if r.owner == nil || !r.roles.BelongsTo(&r.owner.roles.domain) {
		return "", fmt.Errorf("stuntgres: missing regrole output authority")
	}
	role, ok := r.roles.Find(r.owner.roles.domain.Principal(uint64(id)))
	if !ok {
		return strconv.FormatUint(uint64(id), 10), nil
	}
	if r.quoteAll {
		return quoteIdentifier(role.Name), nil
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
		return "", fmt.Errorf("stuntgres: missing regrole output snapshot")
	}
	return (capturedRegroleOutput{owner: s.commentEngine(), roles: s.commentRoles, quoteAll: s.output.QuoteAllIdentifiers}).FormatRegrole(id)
}

func (r *finalRows) FormatRegrole(id uint32) (string, error) {
	text, ok := r.regroleOutput[id]
	if !ok {
		return "", fmt.Errorf("stuntgres: missing regrole output snapshot")
	}
	return text, nil
}

func captureRegroleOutputValue(rows *finalRows, output capturedRegroleOutput, value sqltype.Value) error {
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
	if _, ok := rows.regroleOutput[id]; ok {
		return nil
	}
	text, err := output.FormatRegrole(id)
	if err != nil {
		return err
	}
	if rows.regroleOutput == nil {
		rows.regroleOutput = make(map[uint32]string)
	}
	rows.regroleOutput[id] = text
	rows.output.Regrole = rows
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
