package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (p *PgTrgmProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	if p != StockPgTrgm || ext.Version != "1.6" {
		return fmt.Errorf("stuntgres: invalid pg_trgm base")
	}
	ext.Members = make([]ids.ObjectAddress, PgTrgmMemberCount)
	for i := range ext.Members {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		class := ids.ProcedureClassID
		if i >= PgTrgmOperatorStart {
			class = ids.OperatorClassID
		}
		ext.Members[i] = ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	// Retain the released declaration/collision order while the immutable
	// recipe uses its own fixed slots. All addresses precede cross-links.
	for _, slot := range [...]int{0, 1, 4, 14, 2, 5, 6, 15, 16, 9, 19, 10, 11, 20, 21, 3, 7, 8, 17, 18, 12, 13, 22, 23} {
		if slot < PgTrgmOperatorStart {
			if err := p.stageProcedure(stage, ext, owner, slot); err != nil {
				return err
			}
		} else if err := p.stageOperator(stage, ext, owner, slot-PgTrgmOperatorStart); err != nil {
			return err
		}
	}
	return nil
}

func (p *PgTrgmProvider) stageProcedure(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, slot int) error {
	member := functions.PgTrgmMembers[slot]
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	procedure := &catalog.Procedure{
		ObjectAddress: ext.Members[slot], ObjectNamespace: ext.TargetNamespace,
		Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name,
		Signature: catalog.ProcedureSignature(member.Signature), RoutineKind: catalog.FunctionRoutine,
		Owner: owner, Result: member.Result, Volatility: member.Volatility, Strict: true,
		Installed:      catalog.InstalledRoutineDeclaration{Parallel: 's'},
		Display:        catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name, Binary: "$libdir/pg_trgm", HasBinary: true},
		Implementation: p,
	}
	if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", member.Name), Context: pgTrgmProcedureContext(slot)}
	}
	return stage.Create(procedure)
}

func (p *PgTrgmProvider) stageOperator(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, index int) error {
	recipe := pgTrgmOperators[index]
	member := functions.PgTrgmMembers[recipe.procedure]
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	op := &catalog.Operator{
		ObjectAddress: ext.Members[PgTrgmOperatorStart+index], ObjectNamespace: ext.TargetNamespace,
		Extension: ext.Address(), ObjectRevision: revision, ObjectName: recipe.name,
		Owner: owner, Left: 25, Right: 25, Result: member.Result,
		Function:   ext.Members[recipe.procedure].ObjectID,
		Commutator: ext.Members[PgTrgmOperatorStart+recipe.commutator].ObjectID,
	}
	if member.Threshold != "" {
		op.Restriction, op.Join = 5040, 5041
	}
	if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "operator " + recipe.name + " already exists", Context: pgTrgmOperatorContext(index)}
	}
	return stage.Create(op)
}

// The final bounded recipe is release 1.6. Diagnostics retain their actual
// producer in the upstream base/strict script, not a fabricated 1.6 filename.
func pgTrgmScript(strict bool) string {
	if strict {
		return "pg_trgm--1.3--1.4.sql"
	}
	return "pg_trgm--1.3.sql"
}
func pgTrgmProcedureContext(slot int) string {
	member := functions.PgTrgmMembers[slot]
	lines := [...]int{18, 23, 42, 6, 28, 47, 52, 11, 16, 75, 87, 92, 39, 44}
	arguments, result, volatility := "text,text", "float4", "IMMUTABLE"
	if slot == 0 {
		arguments, result = "text", "_text"
	} else if member.Result == 16 {
		result, volatility = "bool", "STABLE"
	}
	return fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(%s)\nRETURNS %s\nAS '$libdir/pg_trgm'\nLANGUAGE C STRICT %s PARALLEL SAFE\"\nextension script file \"%s\", near line %d", member.Name, arguments, result, volatility, pgTrgmScript(member.Kernel == 2), lines[slot])
}
func pgTrgmOperatorContext(index int) string {
	recipe := pgTrgmOperators[index]
	member := functions.PgTrgmMembers[recipe.procedure]
	estimators := ""
	if member.Threshold != "" {
		estimators = ",\n        RESTRICT = contsel,\n        JOIN = contjoinsel"
	}
	return fmt.Sprintf("SQL statement \"CREATE OPERATOR %s (\n        LEFTARG = text,\n        RIGHTARG = text,\n        PROCEDURE = %s,\n        COMMUTATOR = '%s'%s\n)\"\nextension script file \"%s\", near line %d", recipe.name, member.Name, pgTrgmOperators[recipe.commutator].name, estimators, pgTrgmScript(member.Kernel == 2), recipe.line)
}
