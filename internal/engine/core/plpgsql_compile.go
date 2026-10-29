package core

import (
	"errors"
	"fmt"
	"strconv"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) CompilePLRoutineLocked(op *session.Operation, auth AuthorizationView, d *catalog.Procedure) error {
	language, ok := auth.Catalog.Get(d.Language).(*catalog.Language)
	if !ok {
		return fmt.Errorf("stuntgres: PL routine has no language authority")
	}
	if err := (catalog.LanguageProvider{}).Validate(auth.Catalog, language); err != nil {
		return err
	}
	kind, err := catalog.ResolveLanguageExecution(auth.Catalog, language)
	if err != nil {
		return err
	}
	if kind != catalog.PLpgSQLLanguage {
		return fmt.Errorf("stuntgres: PL routine has no call capability")
	}
	outputs := []sqltype.Usage{d.SQL.Result}
	for _, slot := range d.SQL.OutputFormals {
		outputs = append(outputs, d.SQL.Formals[slot].Usage)
	}
	if err := proc.ValidatePLSignature(d.SQL.Inputs, outputs, d.SQL.Formals, d.SQL.Name, d.RoutineKind == catalog.ProcedureRoutine); err != nil {
		return err
	}
	raw := d.SQL.PLSyntax
	if raw == nil {
		var err error
		raw, err = parser.ParsePLpgSQL(d.SQL.Source, proc.PLReturnPolicy(d.SQL.Result, len(d.SQL.OutputFormals), d.RoutineKind == catalog.ProcedureRoutine))
		if err != nil {
			return parser.PLBodyError(err, d.SQL.Position)
		}
	}
	var dependencies []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool)
	dependency := func(address ids.ObjectAddress) {
		if address.ObjectID != 0 && !seen[address] {
			seen[address] = true
			dependencies = append(dependencies, address)
		}
	}
	resolve := func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if len(raw.Copy.Parts) == 0 {
			if raw.Type.Name == "record" && raw.Type.Schema == nil {
				return sqltype.Usage{}, true, nil
			}
			usage, err := e.AnalysisHost(op).ResolveAnnotationTypeInput(raw.Type, false, false, false, false)
			return usage, false, err
		}
		if raw.Row {
			parts := raw.Copy.Parts
			annotation := parser.TypeName{Name: parts[len(parts)-1].Text, Parts: parts, Pos: parts[0].Pos}
			if len(parts) == 2 {
				annotation.Schema = &parts[0]
			}
			usage, err := e.AnalysisHost(op).ResolveAnnotationTypeInput(annotation, false, false, false, false)
			if err == nil {
				if !usage.Type.BaseType().IsComposite() {
					err = SQLError("42809", "%ROWTYPE requires a composite type")
				} else if typ, ok := auth.Catalog.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}).(*catalog.Type); ok {
					dependency(typ.OwningRelation)
				}
			}
			return usage, false, err
		}
		parts := raw.Copy.Parts
		if len(parts) < 2 {
			return sqltype.Usage{}, false, SQLError("42704", fmt.Sprintf("variable %q does not exist", parts[0].Text))
		}
		name := parser.ObjectName{Parts: parts[:len(parts)-1]}
		path, err := e.LookupNamespaces(auth, name, analyze.RelationLookup)
		if err != nil {
			return sqltype.Usage{}, false, err
		}
		for i := 0; i < path.Count(); i++ {
			ns := path.At(i)
			address := catalog.DefinitionLookupAt(auth.Catalog, op.Definition, ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text})
			descriptor, _ := catalog.DefinitionRelationAt(auth.Catalog, op.Definition, address)
			if descriptor == nil {
				continue
			}
			for i, column := range descriptor.Columns {
				if column.Name == parts[len(parts)-1].Text {
					dependency(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: address.ObjectID, SubID: descriptor.Attributes[i]})
					return column.Usage, false, nil
				}
			}
			return sqltype.Usage{}, false, SQLError("42703", fmt.Sprintf("column %q does not exist", parts[len(parts)-1].Text))
		}
		return sqltype.Usage{}, false, SQLError("42P01", fmt.Sprintf("relation %q does not exist", catalog.ObjectNameText(name)))
	}
	checkedResolve := func(raw parser.PLType) (sqltype.Usage, bool, error) {
		usage, record, err := resolve(raw)
		if err != nil || record {
			return usage, record, err
		}
		if !usage.Valid() || !usage.Type.Concrete() {
			return sqltype.Usage{}, false, SQLError("0A000", "unsupported PL variable type")
		}
		err = auth.Check(access.Requirement{Object: auth.Ref(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}), Privileges: authorization.UsagePrivilege})
		if err == nil {
			dependency(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
			dependency(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(usage.Collation.ID)})
		}
		return usage, false, err
	}
	checkedCollation := func(usage sqltype.Usage, raw *parser.ObjectName) (sqltype.Usage, error) {
		if usage.Type == nil {
			var ok bool
			usage.Type, ok = auth.Catalog.LookupType(2249)
			if !ok {
				return sqltype.Usage{}, fmt.Errorf("stuntgres: missing PL RECORD type")
			}
		}
		usage, err := e.ResolveColumnCollation(usage, raw, op)
		if err == nil {
			dependency(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(usage.Collation.ID)})
		}
		return usage, err
	}
	program, err := proc.CompilePL(d.SQL, raw, d.RoutineKind == catalog.ProcedureRoutine, checkedCollation, checkedResolve)
	if err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) {
			copy := *diagnostic
			if n, parseErr := strconv.Atoi(copy.Position); parseErr == nil {
				copy.Position = strconv.Itoa(d.SQL.Position.Character + n)
			}
			return &copy
		}
		return err
	}
	d.SQL.PLSyntax, d.SQL.PL = raw, program
	d.DeclarationDependencies = dependencies
	_, err = e.retainPLDiagnosticIdentityLocked(op, auth, d)
	return err
}
