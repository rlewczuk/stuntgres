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
	if d.ACL.Explicit {
		return dump.Statement{}, fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
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
	state, ok := view.LookupType(sqltype.ID(d.Result))
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing aggregate state type")
	}
	stateName, err := p.ColumnDeclaration(view, catalog.Column{Usage: state.Usage()})
	if err != nil {
		return dump.Statement{}, err
	}
	sql.WriteString(") (SFUNC = " + catalog.QuoteIdentifier(supportNS.Name()) + "." + catalog.QuoteIdentifier(support.Name()) + ", STYPE = " + stateName)
	if d.Aggregate.Definition.HasInitial {
		sql.WriteString(", INITCOND = " + dump.CommentLiteral(d.Aggregate.Definition.Initial))
	}
	sql.WriteString(", SSPACE = " + strconv.FormatInt(int64(d.Aggregate.Definition.TransitionSpace), 10) + ");\n")
	return dump.Statement{Owner: owner.Name, SQL: sql.String()}, nil
}
