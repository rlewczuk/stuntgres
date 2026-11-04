package pgcatalog

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called after releasing the engine mutex. The immutable descriptor and captured
// input belong to this execution; provider loading never extends catalog locks.
func captureTimezoneSource(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	d := in.Source
	input := in.Runtime.TimezoneInput()
	var rows [][]sqltype.Value
	offsetIndex := len(d.Columns) - 2
	appendFact := func(name, abbrev string, east int32, daylight bool) error {
		offset, err := sqltype.IntervalValue(d.Columns[offsetIndex].Usage.Type, 0, 0, int64(east)*1000000)
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, len(d.Columns))
		if offsetIndex == 2 {
			row[0] = sqltype.String(d.Columns[0].Usage.Type, name)
		}
		row[offsetIndex-1] = sqltype.String(d.Columns[offsetIndex-1].Usage.Type, abbrev)
		row[offsetIndex] = offset
		row[offsetIndex+1] = sqltype.Boolean(d.Columns[offsetIndex+1].Usage.Type, daylight)
		rows = append(rows, row)
		return nil
	}
	var err error
	switch d.Address().ObjectID {
	case 12005:
		err = sqltype.VisitTimezoneNames(ctx, input.TransactionStart.Unix(), func(f sqltype.TimezoneNameFact) error {
			return appendFact(f.Name, f.Abbrev, f.East, f.IsDST)
		})
	case 12004:
		err = sqltype.VisitTimezoneAbbreviations(ctx, input, func(f sqltype.TimezoneAbbrevFact) error {
			return appendFact("", f.Abbrev, f.East, f.IsDST)
		})
	default:
		return nil, fmt.Errorf("stuntgres: unknown timezone source")
	}
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func nonUpdatableViewError(d *catalog.Relation, operation string) error {
	name := d.Name()
	if d.Address().ObjectID == StatioSysSequencesOID || d.Address().ObjectID == StatioUserSequencesOID {
		// The simple sys/user views rewrite to the non-updatable all view.
		name = "pg_statio_all_sequences"
	}
	if d.Address().ObjectID == StatioSysTablesOID || d.Address().ObjectID == StatioUserTablesOID {
		name = "pg_statio_all_tables"
	}
	if d.Address().ObjectID == StatioUserIndexesOID {
		name = "pg_statio_all_indexes"
	}
	if d.Address().ObjectID == StatSysIndexesOID || d.Address().ObjectID == StatUserIndexesOID {
		name = "pg_stat_all_indexes"
	}
	if d.Address().ObjectID == StatXactSysTablesOID || d.Address().ObjectID == StatXactUserTablesOID {
		name = "pg_stat_xact_all_tables"
	}
	if d.Address().ObjectID == StatSysTablesOID || d.Address().ObjectID == StatUserTablesOID {
		name = "pg_stat_all_tables"
	}
	message := fmt.Sprintf(`cannot update view "%s"`, name)
	action := "updating the view"
	switch operation {
	case "insert":
		message = fmt.Sprintf(`cannot insert into view "%s"`, name)
		action = "inserting into the view"
	case "delete":
		message = fmt.Sprintf(`cannot delete from view "%s"`, name)
		action = "deleting from the view"
	}
	detail := "Views that do not select from a single table or view are not automatically updatable."
	if d.Address().ObjectID == 12004 || d.Address().ObjectID == InfoTransformsOID || d.Address().ObjectID == SeclabelsOID {
		detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	} else if d.Address().ObjectID == InfoColumnColumnUsageOID {
		detail = "Views containing DISTINCT are not automatically updatable."
	} else if d.Address().ObjectID == InfoColumnOptionsOID || d.Address().ObjectID == InfoForeignDataWrapperOptionsOID || d.Address().ObjectID == InfoForeignServerOptionsOID || d.Address().ObjectID == InfoForeignTableOptionsOID {
		detail = "Views that return set-returning functions are not automatically updatable."
	} else if d.Address().ObjectID == StatAllTablesOID || d.Address().ObjectID == StatSysTablesOID || d.Address().ObjectID == StatUserTablesOID || d.Address().ObjectID == StatXactAllTablesOID || d.Address().ObjectID == StatXactSysTablesOID || d.Address().ObjectID == StatXactUserTablesOID {
		detail = "Views containing GROUP BY are not automatically updatable."
	}
	upper := strings.ToUpper(operation)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: detail,
		Hint: "To enable " + action + ", provide an INSTEAD OF " + upper + " trigger or an unconditional ON " + upper + " DO INSTEAD rule."}
}
