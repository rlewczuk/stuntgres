package regobj

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CaptureRowsOutput captures the reg* and aclitem output presentation of
// every value a result returns, once, before the Rows are handed out. The
// internal datums stay OIDs, and retained Rows never consult later catalog
// authority. It reads only the statement's captured catalog view, owner,
// roles and reference path. collationPath is called on the first non-NULL
// regcollation value, and again while it returns no path. The caller holds
// any lock the callback needs.
func CaptureRowsOutput(rows *exec.FinalRows, view catalog.Read, owner *catalog.Owner, roles authorization.Read, path []ids.ObjectAddress, collationPath func() ([]ids.ObjectAddress, error)) error {
	c := rowsOutput{
		rows: rows, view: view, path: path,
		acl:  ACLItemOutput{Owner: owner, Roles: roles},
		role: RoleOutput{Owner: owner, Roles: roles, QuoteAll: rows.Output.QuoteAllIdentifiers},
	}
	for _, value := range rows.Values {
		if err := c.capture(value, collationPath); err != nil {
			return err
		}
	}
	for _, row := range rows.Anonymous {
		for _, value := range row {
			if err := c.capture(value, collationPath); err != nil {
				return err
			}
		}
	}
	if rows.Source != nil {
		for i := 0; i < rows.Count; i++ {
			index := i
			if rows.Selection != nil {
				index = rows.Selection[i]
			}
			row := rows.Source.Row(index)
			for _, slot := range rows.Slots {
				if err := c.capture(row[slot], collationPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// rowsOutput is one CaptureRowsOutput call: its inputs and the collation
// path, once read. The collation path callback is passed to each capture
// rather than kept here, so it does not escape with the inputs.
type rowsOutput struct {
	rows       *exec.FinalRows
	view       catalog.Read
	path       []ids.ObjectAddress
	collations []ids.ObjectAddress
	acl        ACLItemOutput
	role       RoleOutput
}

// capture captures one value: collation and namespace aliases, roles,
// aclitems, then the class, routine, type, operator and text-search
// aliases.
func (c *rowsOutput) capture(value sqltype.Value, collationPath func() ([]ids.ObjectAddress, error)) error {
	if err := c.captureObject(value, collationPath); err != nil {
		return err
	}
	if err := CaptureRoleOutputValue(c.rows, c.role, value); err != nil {
		return err
	}
	if err := CaptureACLItemOutputValue(c.rows, c.acl, value); err != nil {
		return err
	}
	return CaptureClassOutputValue(c.rows, c.view, c.path, value)
}

func (c *rowsOutput) captureObject(value sqltype.Value, collationPath func() ([]ids.ObjectAddress, error)) error {
	if value.IsNull() || !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().ContainsRegcollation() && c.collations == nil {
		var err error
		c.collations, err = collationPath()
		if err != nil {
			return err
		}
	}
	return CaptureObjectOutputValue(c.rows, c.view, c.collations, value)
}
