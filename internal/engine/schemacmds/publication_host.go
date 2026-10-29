package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type publicationHost struct{ core.DDLHost }

func (h publicationHost) PublicationSchema(a ddl.Authority, m parser.PublicationMember) (*catalog.Namespace, error) {
	auth := core.DDLAuthorityOf(a)
	address := ids.ObjectAddress{}
	if m.CurrentSchema {
		path, _, err := core.ExplicitPath(auth)
		if err != nil {
			return nil, err
		}
		if len(path) == 0 {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: "no schema has been selected for CURRENT_SCHEMA"}
		}
		address = path[0]
	} else if m.Schema.Text == "pg_temp" {
		address = auth.TemporaryNamespace()
	} else {
		address = auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: m.Schema.Text})
	}
	ns, ok := auth.Catalog.Namespace(address)
	if !ok {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: fmt.Sprintf(`schema "%s" does not exist`, m.Schema.Text)}
	}
	return ns, nil
}
func (h publicationHost) BindPublicationFilter(a ddl.Authority, r *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (catalog.PolicyPayload, []ids.ObjectAddress, error) {
	auth := core.DDLAuthorityOf(a)
	p, err := core.BindRetainedPredicate(h.E, h.Op, auth, r, raw, input, "PUBLICATION WHERE", "WHERE")
	if err != nil {
		return nil, nil, err
	}
	refs, err := publication.ValidateFilter(h.Context(), auth.Catalog, p, raw.SourcePosition())
	if err != nil {
		return nil, nil, err
	}
	return p, refs, nil
}

func (h publicationHost) PublicationWALLevel() string {
	value, _ := h.Op.Setting("wal_level")
	return value
}
