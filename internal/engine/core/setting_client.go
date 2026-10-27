package core

import (
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// checkClientSetting validates logical identities against operation-coherent
// authority. It never creates descriptors or invokes physical mechanisms.
func checkClientSetting(op *session.Operation, d settings.Definition, v *settings.Value) error {
	switch d.Name {
	case "default_table_access_method", "default_tablespace", "temp_tablespaces", "default_text_search_config":
	default:
		return nil
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return err
	}
	return checkClientSettingAuthority(auth, d, v)
}

// checkClientSettingAuthority consumes already captured authority. DDL callers
// hold Engine.Mu and must not recapture it through OperationAuthorization.
func checkClientSettingAuthority(auth AuthorizationView, d settings.Definition, v *settings.Value) error {
	invalid := func(detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "%s": "%s"`, d.Name, v.Text), Detail: detail}
	}
	switch d.Name {
	case "default_table_access_method":
		if v.Text == "" {
			return invalid(`"default_table_access_method" cannot be empty.`)
		}
		method, ok := auth.Catalog.LookupAccessMethod(v.Text)
		if !ok {
			return invalid(fmt.Sprintf(`Table access method "%s" does not exist.`, v.Text))
		}
		if method.MethodKind() != "t" {
			return SQLError("55000", fmt.Sprintf(`access method "%s" is not of type TABLE`, v.Text))
		}
	case "default_tablespace":
		if v.Text != "" {
			if _, err := ddl.TablespaceRef(auth.View, v.Text); err != nil {
				return invalid(fmt.Sprintf(`Tablespace "%s" does not exist.`, v.Text))
			}
		}
	case "temp_tablespaces":
		names, err := settings.ParseIdentifierList(d.Name, v.Text)
		if err != nil {
			return err
		}
		for _, name := range names {
			if name == "" {
				continue
			}
			ref, err := ddl.TablespaceRef(auth.View, name)
			if err != nil {
				return err
			}
			if err := ddl.CheckAlterTablespaceCreate(auth.View, auth, ref); err != nil {
				return err
			}
		}
	case "default_text_search_config":
		name, err := parser.ParseRegclassName(v.Text)
		if err != nil {
			return invalid("")
		}
		parts := name.Parts
		if len(parts) >= 3 {
			names := make([]string, len(parts))
			for i := range parts {
				names[i] = parts[i].Text
			}
			spelling := strings.Join(names, ".")
			if len(parts) > 3 {
				return SQLError("42601", "improper qualified name (too many dotted names): "+spelling)
			}
			if parts[0].Text != auth.Session.DatabaseName {
				return SQLError("0A000", "cross-database references are not implemented: "+spelling)
			}
			parts = parts[1:]
		}
		selection, err := LookupNamespacesSnapshot(auth, parser.ObjectName{Parts: parts}, analyze.FunctionLookup)
		if err != nil {
			var state *sqlstate.Error
			if errors.As(err, &state) && state.Code == "3F000" {
				return invalid("")
			}
			return err
		}
		var path []ids.ObjectAddress
		if len(parts) == 2 {
			path = []ids.ObjectAddress{selection.Qualified}
		} else {
			path = selection.Path
		}
		var address ids.ObjectAddress
		for _, ns := range path {
			address = auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ns, Name: parts[len(parts)-1].Text})
			if address != (ids.ObjectAddress{}) {
				break
			}
		}
		d, ok := auth.Catalog.TSConfiguration(address)
		if !ok {
			return invalid("")
		}
		v.Text = catalog.Identifier(auth.Catalog.MustNamespace(d.Namespace()).Name()) + "." + catalog.Identifier(d.Name())
	}
	return nil
}
