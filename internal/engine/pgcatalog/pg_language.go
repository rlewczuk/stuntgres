package pgcatalog

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const LanguageOID ids.OID = 2612

func capturePGLanguage(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid language source shape")
	}
	for i, column := range source.Columns {
		id := sqltype.Oid
		switch i {
		case 1:
			id = sqltype.Name
		case 3, 4:
			id = sqltype.Bool
		case 8:
			id = sqltype.ACLItemArray
		}
		owned, ok := view.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign language source type")
		}
	}
	var languages []*catalog.Language
	for _, descriptor := range view.Descriptors() {
		if language, ok := descriptor.(*catalog.Language); ok {
			languages = append(languages, language)
		}
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i].Address().ObjectID < languages[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(languages))
	for _, language := range languages {
		if err := (catalog.LanguageProvider{}).Validate(view, language); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(in.Roles, language); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 9)
		for _, field := range []struct {
			index int
			oid   ids.OID
		}{{0, language.Address().ObjectID}, {2, ids.OID(language.Owner.ID())}, {5, language.Call.ObjectID}, {6, language.Inline.ObjectID}, {7, language.Validator.ObjectID}} {
			value, err := sqltype.OIDValue(source.Columns[field.index].Usage.Type, uint32(field.oid))
			if err != nil {
				return nil, err
			}
			row[field.index] = value
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, language.Name())
		row[3] = sqltype.Boolean(source.Columns[3].Usage.Type, language.Procedural)
		row[4] = sqltype.Boolean(source.Columns[4].Usage.Type, language.Trusted)
		acl, err := ClassACL(in.Owner, in.Roles, source.Columns[8].Usage.Type, language.ACL)
		if err != nil {
			return nil, err
		}
		row[8] = acl
		rows = append(rows, row)
	}
	return rows, ctx.Err()
}
