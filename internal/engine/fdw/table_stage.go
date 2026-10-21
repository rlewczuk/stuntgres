package fdw

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StageTable stages CREATE or DROP FOREIGN TABLE. CREATE stages the table
// through table.StageCreateTable and returns one validator job for the table
// and one per column; DROP returns the dropped roots, whose dependents the
// engine stages. A nil, nil, nil result means there is nothing to publish.
func StageTable(h Host, auth ddl.Authority, s *parser.ForeignDDL, input sqltype.InputEnvironment) ([]Validation, []ids.ObjectAddress, error) {
	if s.Action == "drop" {
		return stageTableDrop(h, auth, s)
	}
	view := h.AccessView(auth)
	ns, err := h.ExistingCreationNamespace(auth, s.Name)
	if err != nil {
		return nil, nil, err
	}
	if err = view.CheckCreate(auth, ns); err != nil {
		return nil, nil, ddl.CreationError(err, s.Name)
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	if h.Stage().LookupName(key) != (ids.ObjectAddress{}) {
		if s.IfNotExists {
			h.EmitNotice(session.LevelNotice, session.ExistingRelation(key.Name))
			return nil, nil, nil
		}
		return nil, nil, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	server, err := Find(view.Catalog, ids.ForeignServerClassID, s.Server.Text)
	if err != nil {
		return nil, nil, err
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ObjectRef(view, server), Privileges: authorization.UsagePrivilege}); err != nil {
		return nil, nil, err
	}
	if err = CheckTableDeclaration(s.Table); err != nil {
		return nil, nil, err
	}
	if err = table.StageCreateTable(h, auth, TableSyntax(s.Table), ns.Address, view.State.CurrentUser, input, nil); err != nil {
		return nil, nil, err
	}
	d, ok := h.Stage().Get(h.Stage().LookupName(key)).(*catalog.Relation)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing staged foreign relation")
	}
	d.RelationKind = catalog.ForeignRelation
	d.Foreign = &catalog.ForeignTableDefinition{Server: server.Address(), Columns: make(map[int32][]catalog.ForeignOption)}
	d.Foreign.Options, err = TransformOptions(nil, s.Options)
	if err != nil {
		return nil, nil, err
	}
	wrapper, ok := view.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing foreign-data wrapper")
	}
	h.AddDomainBase(server)
	h.AddDomainBase(wrapper)
	job, err := stageValidatorJob(h, wrapper, catalog.ForeignTable, d.Foreign.Options)
	if err != nil {
		return nil, nil, err
	}
	jobs := []Validation{job}
	for _, attribute := range d.Attributes {
		column := d.Columns[d.IndexForAttribute(attribute)]
		options, er := TransformOptions(nil, s.ColumnOptions[column.Name])
		if er != nil {
			return nil, nil, er
		}
		d.Foreign.Columns[attribute] = options
		job, er := stageValidatorJob(h, wrapper, catalog.ForeignAttribute, options)
		if er != nil {
			return nil, nil, er
		}
		jobs = append(jobs, job)
	}
	return jobs, nil, nil
}

// stageTableDrop stages DROP FOREIGN TABLE: IF EXISTS skips missing names
// with a notice, every target must be a foreign table its actor owns.
func stageTableDrop(h Host, auth ddl.Authority, s *parser.ForeignDDL) ([]Validation, []ids.ObjectAddress, error) {
	view := h.AccessView(auth)
	var roots []ids.ObjectAddress
	for _, name := range s.Names {
		ref, err := h.ResolveRelation(auth, name)
		if err != nil {
			if s.IfExists && table.RenameMissing(err) {
				ddl.Notice(h, "00000", fmt.Sprintf(`foreign table "%s" does not exist, skipping`, catalog.ObjectNameText(name)))
				continue
			}
			return nil, nil, err
		}
		d := view.Catalog.MustRelation(ref.Address)
		if d.RelationKind != catalog.ForeignRelation {
			return nil, nil, sqlError("42809", fmt.Sprintf(`"%s" is not a foreign table`, d.Name()))
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, nil, err
		}
		if err := h.RemoveObject(ref, false); err != nil {
			return nil, nil, err
		}
		roots = append(roots, d.Address())
	}
	return nil, roots, nil
}
