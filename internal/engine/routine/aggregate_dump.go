package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func aggregateDumpStatement(p dump.Presenter, view catalog.Read, roles authorization.Read, d *catalog.Procedure) (dump.Statement, error) {
	if err := ValidateAggregateDefinition(view, d); err != nil {
		return dump.Statement{}, err
	}
	owner, err := roles.Descriptor(d.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	ns, ok := view.Namespace(d.Namespace())
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate namespace")
	}
	support, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Aggregate.Definition.Functions[0]})
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate support routine")
	}
	supportNS, ok := view.Namespace(support.Namespace())
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate support namespace")
	}
	inputs, err := AggregateSignature(view, d.Signature)
	if err != nil {
		return dump.Statement{}, err
	}
	var sql strings.Builder
	sql.WriteString("CREATE AGGREGATE " + catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name()) + "(")
	if len(inputs) == 0 {
		sql.WriteByte('*')
	}
	for i, input := range inputs {
		if i > 0 {
			sql.WriteString(", ")
		}
		name, err := p.ColumnDeclaration(view, catalog.Column{Usage: input})
		if err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString(name)
	}
	state, ok := view.LookupType(sqltype.ID(d.Aggregate.Definition.TransitionType))
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate state type")
	}
	stateName, err := p.ColumnDeclaration(view, catalog.Column{Usage: state.Usage()})
	if err != nil {
		return dump.Statement{}, err
	}
	sql.WriteString(") (SFUNC = " + catalog.QuoteIdentifier(supportNS.Name()) + "." + catalog.QuoteIdentifier(support.Name()) + ", STYPE = " + stateName)
	if oid := d.Aggregate.Definition.Functions[1]; oid != 0 {
		final, found := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if !found {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate final routine")
		}
		finalNS, found := view.Namespace(final.Namespace())
		if !found {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate final namespace")
		}
		sql.WriteString(", FINALFUNC = " + catalog.QuoteIdentifier(finalNS.Name()) + "." + catalog.QuoteIdentifier(final.Name()))
	}
	if d.Aggregate.Definition.FinalExtra {
		sql.WriteString(", FINALFUNC_EXTRA = true")
	}
	mode := "READ_ONLY"
	switch d.Aggregate.Definition.FinalModify {
	case "s":
		mode = "SHAREABLE"
	case "w":
		mode = "READ_WRITE"
	}
	sql.WriteString(", FINALFUNC_MODIFY = " + mode)
	if d.Aggregate.Definition.HasInitial {
		sql.WriteString(", INITCOND = " + dump.CommentLiteral(d.Aggregate.Definition.Initial))
	}
	sql.WriteString(", SSPACE = " + strconv.FormatInt(int64(d.Aggregate.Definition.TransitionSpace), 10))
	definition := &d.Aggregate.Definition
	if definition.MovingType != 0 {
		state, found := view.LookupType(sqltype.ID(definition.MovingType))
		if !found {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing moving aggregate state")
		}
		name, err := p.ColumnDeclaration(view, catalog.Column{Usage: state.Usage()})
		if err != nil {
			return dump.Statement{}, err
		}
		sql.WriteString(", MSTYPE = " + name)
		for _, support := range []struct {
			option string
			oid    ids.OID
		}{
			{"MSFUNC", definition.Functions[5]}, {"MINVFUNC", definition.Functions[6]}, {"MFINALFUNC", definition.Functions[7]},
		} {
			if support.oid == 0 {
				continue
			}
			procedure, found := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: support.oid})
			if !found {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing moving aggregate support")
			}
			namespace, found := view.Namespace(procedure.Namespace())
			if !found {
				return dump.Statement{}, fmt.Errorf("stuntgres: missing moving support namespace")
			}
			sql.WriteString(", " + support.option + " = " + catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(procedure.Name()))
		}
		if definition.HasMovingInitial {
			sql.WriteString(", MINITCOND = " + dump.CommentLiteral(definition.MovingInitial))
		}
		sql.WriteString(", MSSPACE = " + strconv.FormatInt(int64(definition.MovingSpace), 10))
	}
	if definition.MovingFinalExtra {
		sql.WriteString(", MFINALFUNC_EXTRA = true")
	}
	mode = "READ_ONLY"
	switch definition.MovingFinalModify {
	case "s":
		mode = "SHAREABLE"
	case "w":
		mode = "READ_WRITE"
	}
	sql.WriteString(", MFINALFUNC_MODIFY = " + mode + ");\n")
	return dump.Statement{Owner: owner.Name, SQL: sql.String()}, nil
}
