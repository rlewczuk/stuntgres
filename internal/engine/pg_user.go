package engine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Capture from the statement's immutable role root, never from live engine roles.
// Password verifiers are deliberately not consulted: the public value is constant.
func capturePGUser(ctx context.Context, roles RoleRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	for principal, role := range roles.state.roles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !role.Login || principal.id > math.MaxUint32 {
			continue
		}
		id, err := sqltype.OIDValue(source.columns[1].Usage.Type, uint32(principal.id))
		if err != nil {
			return nil, err
		}
		expiry := sqltype.Null(source.columns[7].Usage.Type)
		if role.HasValidUntil {
			expiry, err = sqltype.TimestampValue(source.columns[7].Usage.Type, role.ValidUntil)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(source.columns[0].Usage.Type, role.Name), id,
			sqltype.Boolean(source.columns[2].Usage.Type, role.CreateDB),
			sqltype.Boolean(source.columns[3].Usage.Type, role.Superuser),
			sqltype.Boolean(source.columns[4].Usage.Type, role.Replication),
			sqltype.Boolean(source.columns[5].Usage.Type, role.BypassRLS),
			sqltype.String(source.columns[6].Usage.Type, "********"), expiry,
			sqltype.Null(source.columns[8].Usage.Type),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Target slots are already resolved by ordinary mutation binding. Readonly
// rejection follows semantic analysis and does not depend on values or row demand.
func virtualViewWriteError(d *relationDescriptor, operation string, targets []int) error {
	if d.address.ObjectID == 12008 {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	if d.address.ObjectID != 12014 {
		return timezoneViewWriteError(d, operation)
	}
	verb, action := "update", "updating"
	switch operation {
	case "insert":
		verb, action = "insert into", "inserting into"
	case "delete":
		verb, action = "delete from", "deleting from"
	}
	for _, slot := range targets {
		if d.attributes[slot] == 7 {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("cannot %s column \"passwd\" of view \"pg_user\"", verb), Detail: "View columns that are not columns of their base relation are not updatable."}
		}
	}
	upper := strings.ToUpper(operation)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf("cannot %s view \"pg_shadow\"", verb), Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + upper + " trigger or an unconditional ON " + upper + " DO INSTEAD rule."}
}
