package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func xml2Display(slot int) catalog.RoutineDisplay {
	if slot == 0 {
		return catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "xml_is_well_formed"}
	}
	return catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: functions.XML2Members[slot].Name, Binary: "$libdir/pgxml", HasBinary: true}
}

// Context is released script provenance, independent of the current base version.
func xml2CollisionContext(slot int) string {
	switch slot {
	case 0:
		return "SQL statement \"CREATE FUNCTION xml_valid(text) RETURNS bool\nAS 'xml_is_well_formed'\nLANGUAGE INTERNAL STRICT STABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 9"
	case 1:
		return "SQL statement \"CREATE FUNCTION xpath_string(text,text) RETURNS text\nAS '$libdir/pgxml'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 19"
	case 2:
		return "SQL statement \"CREATE FUNCTION xpath_number(text,text) RETURNS real\nAS '$libdir/pgxml'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 24"
	default:
		return "SQL statement \"CREATE FUNCTION xpath_bool(text,text) RETURNS boolean\nAS '$libdir/pgxml'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"xml2--1.1.sql\", near line 29"
	}
}
func (p *XML2Provider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 0, len(functions.XML2Members))
	for slot, m := range functions.XML2Members {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: m.Name, Signature: catalog.ProcedureSignature(m.Signature), Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(m.Result), Volatility: m.Volatility, Strict: true, Installed: catalog.InstalledRoutineDeclaration{Parallel: 's'}, Display: xml2Display(slot), Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name()), Context: xml2CollisionContext(slot)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members = append(ext.Members, address)
	}
	return nil
}
