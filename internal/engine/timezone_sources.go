package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called after releasing the engine mutex. The immutable descriptor and captured
// input belong to this execution; provider loading never extends catalog locks.
func captureTimezoneSource(ctx context.Context, ref ids.ObjectRef, d *relationDescriptor, input sqltype.InputEnvironment) (*querySource, error) {
	var rows [][]sqltype.Value
	offsetIndex := len(d.columns) - 2
	appendFact := func(name, abbrev string, east int32, daylight bool) error {
		offset, err := sqltype.IntervalValue(d.columns[offsetIndex].Usage.Type, 0, 0, int64(east)*1000000)
		if err != nil {
			return err
		}
		row := make([]sqltype.Value, len(d.columns))
		if offsetIndex == 2 {
			row[0] = sqltype.String(d.columns[0].Usage.Type, name)
		}
		row[offsetIndex-1] = sqltype.String(d.columns[offsetIndex-1].Usage.Type, abbrev)
		row[offsetIndex] = offset
		row[offsetIndex+1] = sqltype.Boolean(d.columns[offsetIndex+1].Usage.Type, daylight)
		rows = append(rows, row)
		return nil
	}
	var err error
	switch d.address.ObjectID {
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
	return &querySource{ref: ref, anonymous: rows}, nil
}

func timezoneViewWriteError(d *relationDescriptor, operation string) error {
	message := fmt.Sprintf(`cannot update view "%s"`, d.name)
	action := "updating the view"
	switch operation {
	case "insert":
		message = fmt.Sprintf(`cannot insert into view "%s"`, d.name)
		action = "inserting into the view"
	case "delete":
		message = fmt.Sprintf(`cannot delete from view "%s"`, d.name)
		action = "deleting from the view"
	}
	detail := "Views that do not select from a single table or view are not automatically updatable."
	if d.address.ObjectID == 12004 {
		detail = "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."
	}
	upper := strings.ToUpper(operation)
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: detail,
		Hint: "To enable " + action + ", provide an INSTEAD OF " + upper + " trigger or an unconditional ON " + upper + " DO INSTEAD rule."}
}
