package extension

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func postgresOptionValue(options []catalog.ForeignOption, name, fallback string) string {
	for _, option := range options {
		if option.Name == name {
			return option.Value
		}
	}
	return fallback
}
func postgresSQLError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func (*PostgresFDWProvider) GetForeignRelSize(ctx context.Context, request catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: 1000, Width: float64(32 * len(request.RequiredAttributes))}, ctx.Err()
}
func (*PostgresFDWProvider) GetForeignPaths(ctx context.Context, request catalog.ForeignRequest, size catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cost := func(name, fallback string) (float64, error) {
		text := postgresOptionValue(request.ServerOptions, name, fallback)
		value, suffix, valid := settings.ParseNumber(text, false)
		if !valid || suffix != "" {
			return 0, postgresSQLError("22023", fmt.Sprintf("invalid value for floating point option %q: %s", name, text))
		}
		if value < 0 {
			return 0, postgresSQLError("22023", fmt.Sprintf("%q must be a floating point value greater than or equal to zero", name))
		}
		// The callback protocol requires finite estimates. Saturation represents
		// an admitted unbounded cost, rather than rejecting the stored option.
		return min(value, math.MaxFloat64), nil
	}
	startup, err := cost("fdw_startup_cost", "100")
	if err != nil {
		return nil, err
	}
	tuple, err := cost("fdw_tuple_cost", "0.2")
	if err != nil {
		return nil, err
	}
	total := min(startup+size.Rows*tuple, math.MaxFloat64)
	return []catalog.ForeignPath{{StartupCost: startup, TotalCost: total, Attributes: slices.Clone(request.RequiredAttributes)}}, nil
}
func (*PostgresFDWProvider) GetForeignPlan(ctx context.Context, _ catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{Attributes: slices.Clone(path.Attributes)}, ctx.Err()
}
func postgresFetchSize(request catalog.ForeignRequest) (int32, error) {
	text := postgresOptionValue(request.TableOptions, "fetch_size", postgresOptionValue(request.ServerOptions, "fetch_size", "100"))
	n, suffix, valid := settings.ParseNumber(text, true)
	n = math.RoundToEven(n)
	if !valid || suffix != "" || n < math.MinInt32 || n > math.MaxInt32 {
		return 0, postgresSQLError("22023", `invalid value for integer option "fetch_size": `+text)
	}
	if n <= 0 {
		return 0, postgresSQLError("22023", `"fetch_size" must be an integer value greater than zero`)
	}
	return int32(n), nil
}

func (*PostgresFDWProvider) BeginForeignScan(ctx context.Context, request catalog.ForeignRequest, plan catalog.ForeignPlan) (catalog.ForeignScan, error) {
	tx, ok := request.Transaction.(*postgresFDWTransaction)
	if !ok || tx.lease == nil {
		return nil, errors.New("stuntgres: postgres_fdw transaction missing")
	}
	capability, ok := request.ExternalSQL.(catalog.PostgresFDWSession)
	if !ok {
		return nil, &catalog.ExternalSQLUnavailableError{Capability: "postgres_fdw local column input"}
	}
	fetch, err := postgresFetchSize(request)
	if err != nil {
		return nil, err
	}
	columns := make([]string, len(plan.Attributes))
	var byteaColumns []int32
	for i, attribute := range plan.Attributes {
		base, err := capability.LocalColumnBaseType(attribute)
		if err != nil {
			return nil, err
		}
		if base == sqltype.Bytea {
			if byteaColumns == nil {
				byteaColumns = make([]int32, len(plan.Attributes))
			}
			byteaColumns[i] = attribute
		}
		found := false
		for _, column := range request.Columns {
			if column.Attribute == attribute {
				columns[i] = catalog.DependencyIdentifier(postgresOptionValue(column.Options, "column_name", column.Name))
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("stuntgres: postgres_fdw missing projected attribute")
		}
	}
	projection := strings.Join(columns, ", ")
	if len(columns) == 0 {
		projection = "NULL"
	}
	schema := postgresOptionValue(request.TableOptions, "schema_name", request.Schema)
	table := postgresOptionValue(request.TableOptions, "table_name", request.Table)
	query := "SELECT " + projection + " FROM " + catalog.DependencyIdentifier(schema) + "." + catalog.DependencyIdentifier(table)
	if tx.cursor == math.MaxUint64 {
		return nil, errors.New("stuntgres: postgres_fdw cursor identity exhausted")
	}
	tx.cursor++
	scan := &postgresFDWScan{tx: tx, query: query, cursor: "c" + strconv.FormatUint(tx.cursor, 10), fetch: fetch, row: make(catalog.ForeignRow, len(columns)), byteaColumns: byteaColumns}
	_, err = tx.connection().Execute(ctx, "DECLARE "+scan.cursor+" SCROLL CURSOR FOR "+query)
	if err != nil {
		scan.failed = true
		return scan, postgresRemoteError(err, query)
	}
	return scan, nil
}

// Stock postgres_fdw reports remote logical diagnostics plus the deparsed SELECT,
// not libpq's remote character/internal positions or the local cursor command.
func postgresRemoteError(err error, query string) error {
	var remote *sqlstate.Error
	if !errors.As(err, &remote) {
		return err
	}
	d := *remote
	d.Position, d.InternalPosition, d.InternalQuery = "", "", ""
	if d.Context != "" {
		d.Context += "\n"
	}
	d.Context += "remote SQL command: " + query
	return sqlstate.WithDiagnostic(err, &d)
}

type postgresFDWScan struct {
	tx                        *postgresFDWTransaction
	query, cursor             string
	fetch                     int32
	result                    catalog.ExternalSQLResult
	index                     int
	row                       catalog.ForeignRow
	byteaColumns              []int32 // Zero means non-BYTEA; otherwise the admitted stable attribute.
	failed, exhausted, closed bool
}

func (s *postgresFDWScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if s.closed {
		return nil, false, errors.New("stuntgres: postgres_fdw scan is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.index == s.result.RowCount {
		if s.exhausted {
			return nil, false, nil
		}
		results, err := s.tx.connection().Execute(ctx, "FETCH "+strconv.FormatInt(int64(s.fetch), 10)+" FROM "+s.cursor)
		if err != nil {
			s.failed = true
			return nil, false, postgresRemoteError(err, s.query)
		}
		if len(results) != 1 || !results[0].Tuple || len(results[0].Columns) != max(1, len(s.row)) {
			s.failed = true
			return nil, false, errors.New("stuntgres: postgres_fdw remote descriptor mismatch")
		}
		s.result, s.index = results[0], 0
		s.exhausted = s.result.RowCount < int(s.fetch)
		if s.result.RowCount == 0 {
			return nil, false, nil
		}
	}
	width := len(s.result.Columns)
	for i := range s.row {
		cell := s.result.Cells[s.index*width+i]
		s.row[i] = nil
		if cell.Null {
			continue
		}
		if s.byteaColumns != nil && s.byteaColumns[i] != 0 {
			value, err := s.tx.lease.ByteaDescriptor().Input(ctx, cell.Text, -1)
			if err != nil {
				s.failed = true
				return nil, false, &catalog.ForeignColumnInputError{Attribute: s.byteaColumns[i], Err: err}
			}
			raw, err := value.Binary()
			if err != nil {
				s.failed = true
				return nil, false, &catalog.ForeignColumnInputError{Attribute: s.byteaColumns[i], Err: err}
			}
			s.row[i] = raw
		} else {
			s.row[i] = cell.Text
		}
	}
	s.index++
	return s.row, true, nil
}
func (s *postgresFDWScan) ReScanForeignScan(ctx context.Context, _ catalog.ForeignScanParameters) error {
	if s.closed || s.failed {
		return errors.New("stuntgres: postgres_fdw scan cannot rewind")
	}
	if _, err := s.tx.connection().Execute(ctx, "MOVE BACKWARD ALL FROM "+s.cursor); err != nil {
		s.failed = true
		return postgresRemoteError(err, s.query)
	}
	s.result, s.index, s.exhausted = catalog.ExternalSQLResult{}, 0, false
	clear(s.row)
	return nil
}
func (s *postgresFDWScan) EndForeignScan(ctx context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.result, s.row = catalog.ExternalSQLResult{}, nil
	s.byteaColumns = nil
	if s.failed {
		return nil
	} // The existing statement rollback owns failed cursors.
	_, err := s.tx.connection().Execute(ctx, "CLOSE "+s.cursor)
	if err != nil {
		return postgresRemoteError(err, s.query)
	}
	return nil
}
