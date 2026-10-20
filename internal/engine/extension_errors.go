package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func extensionMemberConflict(d catalog.Descriptor, ns ids.ObjectAddress) error {
	err := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR"}
	switch d := d.(type) {
	case *catalog.Procedure:
		err.Code = "42723"
		err.Message = fmt.Sprintf(`function "%s" already exists with same argument types`, d.Name())
		args, result, symbol, properties, line := "text", "text", "unaccent_dict", "STABLE STRICT PARALLEL SAFE", 11
		switch d.Signature {
		case catalog.UnaccentDictionarySignature:
			args, line = "regdictionary, text", 6
		case catalog.UnaccentInitSignature:
			args, result, symbol, properties, line = "internal", "internal", "unaccent_init", "PARALLEL SAFE", 16
		case catalog.UnaccentLexizeSignature:
			args, result, symbol, properties, line = "internal,internal,internal,internal", "internal", "unaccent_lexize", "PARALLEL SAFE", 21
		}
		err.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\n\tRETURNS %s\n\tAS '$libdir/unaccent', '%s'\n\tLANGUAGE C %s\"\nextension script file \"unaccent--1.1.sql\", near line %d", d.Name(), args, result, symbol, properties, line)
	case *catalog.TSTemplate:
		err.Code = "23505"
		err.Schema = "pg_catalog"
		err.Table = "pg_ts_template"
		err.Constraint = "pg_ts_template_tmplname_index"
		err.Detail = fmt.Sprintf("Key (tmplname, tmplnamespace)=(unaccent, %d) already exists.", ns.ObjectID)
		err.Context = unaccentTemplateContext
	case *catalog.TSDictionary:
		err.Code = "23505"
		err.Schema = "pg_catalog"
		err.Table = "pg_ts_dict"
		err.Constraint = "pg_ts_dict_dictname_index"
		err.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(unaccent, %d) already exists.", ns.ObjectID)
		err.Context = "SQL statement \"CREATE TEXT SEARCH DICTIONARY unaccent (\n\tTEMPLATE = unaccent,\n\tRULES    = 'unaccent'\n)\"\nextension script file \"unaccent--1.1.sql\", near line 31"
	}
	if err.Code == "23505" {
		err.Message = fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, err.Constraint)
	}
	return err
}

func extensionNameConflict(name string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_extension_name_index"`, Detail: fmt.Sprintf("Key (extname)=(%s) already exists.", name), Schema: "pg_catalog", Table: "pg_extension", Constraint: "pg_extension_name_index"}
}

func extensionVersionError(version string) error {
	if detail := catalog.ExtensionNameDetail(version, true); detail != "" {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid extension version name: "%s"`, version), Detail: detail}
	}
	return nil
}
