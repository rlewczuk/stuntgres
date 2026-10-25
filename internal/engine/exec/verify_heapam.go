package exec

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type HeapVerificationOptions struct {
	Relation ids.OID
	Stop bool
	Physical bool
}

func VerifyHeapamOptions(args [6]sqltype.Value) (HeapVerificationOptions, error) {
	var options HeapVerificationOptions
	for i, name := range [...]string{"relation", "on_error_stop", "check_toast", "skip"} {
		if args[i].IsNull() { return options, sqlError("22023", name+" cannot be null") }
	}
	skip, err := args[3].Text()
	if err != nil { return options, err }
	switch strings.ToLower(skip) {
	case "none", "all-visible", "all-frozen":
	default:
		return options, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid skip option", Hint: `Valid skip options are "all-visible", "all-frozen", and "none".`}
	}
	text, err := args[0].Text()
	if err != nil { return options, err }
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil { return options, fmt.Errorf("stuntgres: unresolved verify_heapam regclass: %w", err) }
	options.Relation = ids.OID(oid)
	options.Stop, err = args[1].Boolean()
	if err != nil { return options, err }
	toast, err := args[2].Boolean()
	if err != nil { return options, err }
	options.Physical = toast || !strings.EqualFold(skip, "none") || !args[4].IsNull() || !args[5].IsNull()
	return options, nil
}

func CheckVerifyHeapamTarget(view catalog.Read, d *catalog.Relation, options HeapVerificationOptions) error {
	var kind string
	switch d.RelationKind {
	case catalog.TableRelation, catalog.MaterializedViewRelation, catalog.SequenceRelation:
	case catalog.PartitionedRelation: kind = "partitioned tables"
	case catalog.IndexRelation: kind = "indexes"
	case catalog.ViewRelation: kind = "views"
	case catalog.ForeignRelation: kind = "foreign tables"
	case catalog.CompositeRelation: kind = "composite types"
	case catalog.CatalogRelation:
		if pgcatalog.ClassKind(view, d) == "v" { kind = "views" } else { return sqlError("0A000", "verify_heapam requires stored heap data") }
	default:
		return sqlError("0A000", "verify_heapam requires stored heap data")
	}
	if kind != "" { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot check relation "%s"`, d.Name()), Detail: "This operation is not supported for "+kind+"."} }
	if options.Physical { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "verify_heapam physical heap options are not supported", Detail: "Native storage has no physical heap blocks, visibility map, or external TOAST chains."} }
	return nil
}

func VerifyHeapamPrefix(ctx context.Context, view *visibility.View, ref ids.ObjectRef, d *catalog.Relation, stop bool) ([]storage.HeapFinding, error) {
	source, err := CapturePrefix(view, ref)
	if err != nil { return nil, err }
	defer source.Close()
	return storage.VerifyHeapam(ctx, storage.HeapVerification{Columns: d.Columns, Attributes: d.Attributes, Committed: source.Committed, Own: source.Own, Ends: source.View.Data().Ends[ref.Address]}, stop)
}

func VerifyHeapamValues(ctx context.Context, result *sqltype.Descriptor, findings []storage.HeapFinding) ([]sqltype.Value, error) {
	rows := make([]sqltype.Value, len(findings))
	for i, finding := range findings {
		if err := ctx.Err(); err != nil { return nil, err }
		fields := make([]sqltype.Value, 4)
		for j := range 3 { fields[j] = sqltype.Null(result.CompositeMember(j).Usage.Type) }
		var err error
		if finding.Attribute != 0 {
			fields[2], err = sqltype.Integer(result.CompositeMember(2).Usage.Type, int64(finding.Attribute))
			if err != nil { return nil, err }
		}
		fields[3] = sqltype.String(result.CompositeMember(3).Usage.Type, finding.Message)
		rows[i], err = sqltype.OwnedCompositeValue(result, fields)
		if err != nil { return nil, err }
	}
	return rows, nil
}
