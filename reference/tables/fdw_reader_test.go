package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// fdwSQLReader reads an independent, real SQL database. Its rows and schema are
// produced by source SQL, never by a request-to-expected-result lookup table.
type fdwSQLReader struct {
	driver                                                                                    *stuntgres.Driver
	source                                                                                    *sql.DB
	dsn                                                                                       string
	mu                                                                                        sync.Mutex
	begins, rescans, ends, generations, queries, iterations, openRows, openCursors, openPools int
	peakOpenCursors                                                                           int
	parameters                                                                                []stuntgres.ForeignScanParameters
	calls                                                                                     int
	validations                                                                               []fdwValidation
	validateHook                                                                              func(context.Context, stuntgres.ForeignObjectKind, []stuntgres.ForeignOption) error
	rowTransform                                                                              func(context.Context, stuntgres.ForeignRow) (stuntgres.ForeignRow, error)
	hook                                                                                      func(context.Context, string) error
	writer                                                                                    *fdwSQLWriter
	parallel                                                                                  *fdwParallelReader
	importDefinitionHook                                                                      func(context.Context, *stuntgres.ForeignTableDefinition) error
}

func newFDWSQLReader(t *testing.T) *fdwSQLReader {
	t.Helper()
	d := stuntgres.NewDriver()
	dsn := "fdw-independent/" + t.Name()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	r := &fdwSQLReader{driver: d, dsn: dsn, source: sql.OpenDB(connector)}
	t.Cleanup(func() {
		if err := r.source.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := r.source.ExecContext(context.Background(), "CREATE ROLE stuntgres SUPERUSER LOGIN"); err != nil {
		t.Fatal(err)
	}
	return r
}
func (r *fdwSQLReader) configure(d *stuntgres.Driver) error {
	var wrapper stuntgres.ForeignDataWrapper = r
	if r.writer != nil {
		wrapper = r.writer
	}
	if r.parallel != nil {
		wrapper = r.parallel
		if r.parallel.transaction != nil {
			wrapper = r.parallel.transaction
		}
	}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_sql_handler"}, Wrapper: wrapper}); err != nil {
		return err
	}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_scan_only_handler"}, Wrapper: fdwScanOnly{r}}); err != nil {
		return err
	}
	if r.writer != nil {
		if err := r.writer.registerVariants(d); err != nil {
			return err
		}
	}
	return d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_sql_validator"}, Validate: r.validate})
}

// Expose exactly the actual scan interface, intentionally not the reader's
// optional schema-import capability. Promoted methods still execute real SQL.
type fdwScanOnly struct{ stuntgres.ForeignDataWrapper }

func (r *fdwSQLReader) call(ctx context.Context, method string) error {
	r.mu.Lock()
	r.calls++
	hook := r.hook
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx, method)
	}
	return ctx.Err()
}

type fdwValidation struct {
	kind       stuntgres.ForeignObjectKind
	options    []stuntgres.ForeignOption
	nilOptions bool
}

func (r *fdwSQLReader) validate(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
	r.mu.Lock()
	r.validations = append(r.validations, fdwValidation{kind, append([]stuntgres.ForeignOption{}, options...), options == nil})
	hook := r.validateHook
	r.mu.Unlock()
	if err := r.call(ctx, "Validate"); err != nil {
		return err
	}
	for _, option := range options {
		valid := kind == stuntgres.ForeignTable && (option.Name == "schema_name" || option.Name == "table_name") ||
			kind == stuntgres.ForeignAttribute && option.Name == "column_name" ||
			kind == stuntgres.ForeignUserMapping && option.Name == "user"
		if !valid {
			return fmt.Errorf("SQL reader: unsupported option %q for object %d", option.Name, kind)
		}
	}
	if hook != nil {
		return hook(ctx, kind, options)
	}
	return nil
}

// Graph-fixture provenance and synthetic credential options are opaque metadata,
// not a claim that native SQL connectors implement password transport.
func (r *fdwSQLReader) validateGraph(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
	if err := r.call(ctx, "Validate"); err != nil {
		return err
	}
	for _, option := range options {
		valid := (kind == stuntgres.ForeignWrapper || kind == stuntgres.ForeignServer) && option.Name == "tag" ||
			kind == stuntgres.ForeignUserMapping && (option.Name == "user" || option.Name == "password" || option.Name == "empty") ||
			kind == stuntgres.ForeignTable && (option.Name == "schema_name" || option.Name == "table_name") ||
			kind == stuntgres.ForeignAttribute && option.Name == "column_name"
		if !valid {
			return fmt.Errorf("SQL graph provider: unsupported option %q for object %d", option.Name, kind)
		}
	}
	r.mu.Lock()
	hook := r.validateHook
	r.mu.Unlock()
	if hook != nil {
		return hook(ctx, kind, options)
	}
	return ctx.Err()
}
func (r *fdwSQLReader) setHook(hook func(context.Context, string) error) {
	r.mu.Lock()
	r.hook = hook
	r.mu.Unlock()
}

type fdwReaderState struct{ calls, begins, rescans, ends, generations, queries, iterations, openRows, openCursors, openPools int }

func (r *fdwSQLReader) snapshot() fdwReaderState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fdwReaderState{r.calls, r.begins, r.rescans, r.ends, r.generations, r.queries, r.iterations, r.openRows, r.openCursors, r.openPools}
}
func fdwOption(options []stuntgres.ForeignOption, name string) string {
	for _, option := range options {
		if option.Name == name {
			return option.Value
		}
	}
	return ""
}
func fdwQuote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
func fdwRemote(request stuntgres.ForeignRequest) string {
	schema, table := fdwOption(request.TableOptions, "schema_name"), fdwOption(request.TableOptions, "table_name")
	if schema == "" {
		schema = request.Schema
	}
	if table == "" {
		table = request.Table
	}
	return fdwQuote(schema) + "." + fdwQuote(table)
}

type fdwSQLPool struct {
	*sql.DB
	reader     *fdwSQLReader
	once       sync.Once
	closeError error
}

func (p *fdwSQLPool) Close() error {
	p.once.Do(func() { p.closeError = p.DB.Close(); p.reader.mu.Lock(); p.reader.openPools--; p.reader.mu.Unlock() })
	return p.closeError
}
func (r *fdwSQLReader) pool(options []stuntgres.ForeignOption) (*fdwSQLPool, error) {
	role := fdwOption(options, "user")
	if role == "" {
		role = "postgres"
	}
	connector, err := r.driver.OpenConnectorAs(r.dsn, role)
	if err != nil {
		return nil, err
	}
	pool := &fdwSQLPool{DB: sql.OpenDB(connector), reader: r}
	r.mu.Lock()
	r.openPools++
	r.mu.Unlock()
	return pool, nil
}
func (r *fdwSQLReader) GetForeignRelSize(ctx context.Context, request stuntgres.ForeignRequest) (stuntgres.ForeignRelSize, error) {
	if err := r.call(ctx, "GetForeignRelSize"); err != nil {
		return stuntgres.ForeignRelSize{}, err
	}
	pool, err := r.pool(request.MappingOptions)
	if err != nil {
		return stuntgres.ForeignRelSize{}, err
	}
	defer pool.Close()
	var count int64
	if err = pool.QueryRowContext(ctx, "SELECT count(*) FROM "+fdwRemote(request)).Scan(&count); err != nil {
		return stuntgres.ForeignRelSize{}, err
	}
	return stuntgres.ForeignRelSize{Rows: float64(count), Width: float64(len(request.RequiredAttributes) * 8)}, nil
}
func (r *fdwSQLReader) GetForeignPaths(ctx context.Context, request stuntgres.ForeignRequest, size stuntgres.ForeignRelSize) ([]stuntgres.ForeignPath, error) {
	if err := r.call(ctx, "GetForeignPaths"); err != nil {
		return nil, err
	}
	return []stuntgres.ForeignPath{{TotalCost: size.Rows, Attributes: append([]int32{}, request.RequiredAttributes...), Private: fdwRemote(request)}}, nil
}
func (r *fdwSQLReader) GetForeignPlan(ctx context.Context, request stuntgres.ForeignRequest, path stuntgres.ForeignPath) (stuntgres.ForeignPlan, error) {
	if err := r.call(ctx, "GetForeignPlan"); err != nil {
		return stuntgres.ForeignPlan{}, err
	}
	return stuntgres.ForeignPlan{Attributes: append([]int32{}, path.Attributes...), Private: path.Private}, nil
}
func (r *fdwSQLReader) BeginForeignScan(ctx context.Context, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	pool, err := r.pool(request.MappingOptions)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.begins++
	r.parameters = append(r.parameters, request.Parameters)
	r.mu.Unlock()
	var columns []string
	for _, attribute := range plan.Attributes {
		for _, column := range request.Columns {
			if column.Attribute == attribute {
				name := fdwOption(column.Options, "column_name")
				if name == "" {
					name = column.Name
				}
				columns = append(columns, fdwQuote(name))
				break
			}
		}
	}
	projection := strings.Join(columns, ",")
	if len(columns) == 0 {
		projection = "1"
	}
	scan := &fdwSQLScan{reader: r, pool: pool, query: "SELECT " + projection + " FROM " + plan.Private.(string), width: len(columns)}
	return scan, r.call(ctx, "BeginForeignScan")
}

// A real source SQL cursor owns the frontier, not a database/sql.Rows bound to
// the first FETCH's request context. Every Iterate issues a finite FETCH with
// its current context and closes that finalized result before returning.
type fdwSQLScan struct {
	reader             *fdwSQLReader
	pool               *fdwSQLPool
	conn               *sql.Conn
	query              string
	width              int
	cursor, eof, ended bool
}

func (s *fdwSQLScan) open(ctx context.Context) error {
	if s.conn == nil {
		conn, err := s.pool.Conn(ctx)
		if err != nil {
			return err
		}
		s.conn = conn
		if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
	}
	if _, err := s.conn.ExecContext(ctx, "DECLARE fdw_reader_cursor NO SCROLL CURSOR FOR "+s.query); err != nil {
		return err
	}
	s.cursor, s.eof = true, false
	s.reader.mu.Lock()
	s.reader.generations++
	s.reader.openCursors++
	s.reader.peakOpenCursors = max(s.reader.peakOpenCursors, s.reader.openCursors)
	s.reader.mu.Unlock()
	return nil
}
func (s *fdwSQLScan) closeCursor(ctx context.Context) error {
	if !s.cursor {
		return nil
	}
	s.cursor = false
	s.reader.mu.Lock()
	s.reader.openCursors--
	s.reader.mu.Unlock()
	_, err := s.conn.ExecContext(ctx, "CLOSE fdw_reader_cursor")
	return err
}
func (s *fdwSQLScan) IterateForeignScan(ctx context.Context) (row stuntgres.ForeignRow, present bool, err error) {
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.eof {
		return nil, false, nil
	}
	if !s.cursor {
		if err = s.open(ctx); err != nil {
			return nil, false, err
		}
	}
	rows, err := s.conn.QueryContext(ctx, "FETCH 1 FROM fdw_reader_cursor")
	if err != nil {
		return nil, false, err
	}
	s.reader.mu.Lock()
	s.reader.queries++
	s.reader.openRows++
	s.reader.mu.Unlock()
	defer func() {
		closeErr := rows.Close()
		s.reader.mu.Lock()
		s.reader.openRows--
		s.reader.mu.Unlock()
		if err == nil && closeErr != nil {
			row, present, err = nil, false, closeErr
		}
	}()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, false, err
		}
		s.eof = true
		return nil, false, s.closeCursor(ctx)
	}
	width := s.width
	if width == 0 {
		width = 1
	}
	values := make([]any, width)
	dest := make([]any, width)
	for i := range values {
		dest[i] = &values[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return nil, false, err
	}
	cells := make([]driver.Value, width)
	for i, value := range values {
		cells[i] = value
	}
	s.reader.mu.Lock()
	s.reader.iterations++
	transform := s.reader.rowTransform
	s.reader.mu.Unlock()
	if err = s.reader.call(ctx, "IterateForeignScan"); err != nil {
		return nil, false, err
	}
	if s.width == 0 {
		cells = cells[:0]
	}
	if transform != nil {
		row, err = transform(ctx, stuntgres.ForeignRow(cells))
		return row, err == nil, err
	}
	return stuntgres.ForeignRow(cells), true, nil
}
func (s *fdwSQLScan) ReScanForeignScan(ctx context.Context, parameters stuntgres.ForeignScanParameters) error {
	if err := s.closeCursor(ctx); err != nil {
		return err
	}
	s.reader.mu.Lock()
	s.reader.rescans++
	s.reader.parameters = append(s.reader.parameters, parameters)
	s.reader.mu.Unlock()
	if err := s.reader.call(ctx, "ReScanForeignScan"); err != nil {
		return err
	}
	return s.open(ctx)
}
func (s *fdwSQLScan) EndForeignScan(ctx context.Context) error {
	if s.ended {
		return fmt.Errorf("reader scan ended twice")
	}
	s.ended = true
	err := s.closeCursor(ctx)
	if s.conn != nil {
		if _, rollbackErr := s.conn.ExecContext(ctx, "ROLLBACK"); err == nil {
			err = rollbackErr
		}
		if closeErr := s.conn.Close(); err == nil {
			err = closeErr
		}
		s.conn = nil
	}
	if closeErr := s.pool.Close(); err == nil {
		err = closeErr
	}
	s.reader.mu.Lock()
	s.reader.ends++
	s.reader.mu.Unlock()
	hookErr := s.reader.call(ctx, "EndForeignScan")
	if err != nil {
		return err
	}
	return hookErr
}

func (r *fdwSQLReader) ImportForeignSchema(ctx context.Context, request stuntgres.ForeignImportRequest) ([]stuntgres.ForeignTableDefinition, error) {
	if err := r.call(ctx, "ImportForeignSchema"); err != nil {
		return nil, err
	}
	importNotNull := true
	for _, option := range request.Options {
		if option.Name != "import_not_null" {
			return nil, fmt.Errorf("SQL reader: unsupported schema import option %q", option.Name)
		}
		switch option.Value {
		case "true":
			importNotNull = true
		case "false":
			importNotNull = false
		default:
			return nil, fmt.Errorf("SQL reader: invalid import_not_null value %q", option.Value)
		}
	}
	r.mu.Lock()
	definitionHook := r.importDefinitionHook
	r.mu.Unlock()
	pool, err := r.pool(request.MappingOptions)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	var schemas int64
	if err = pool.QueryRowContext(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", request.RemoteSchema).Scan(&schemas); err != nil {
		return nil, err
	}
	if schemas != 1 {
		return nil, &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "HV00Q", Message: fmt.Sprintf(`schema "%s" is not present on foreign server "%s"`, request.RemoteSchema, request.Server)}
	}
	rows, err := pool.QueryContext(ctx, "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind='r' ORDER BY c.relname", request.RemoteSchema)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	var definitions []stuntgres.ForeignTableDefinition
	for _, name := range tables {
		if len(request.Tables) != 0 {
			selected := false
			for _, filter := range request.Tables {
				if filter == name {
					selected = true
					break
				}
			}
			if request.Except {
				selected = !selected
			}
			if !selected {
				continue
			}
		}
		definition, err := fdwSQLImportTable(ctx, pool, request.RemoteSchema, name)
		if err != nil {
			return nil, err
		}
		if !importNotNull {
			for i := range definition.Columns {
				definition.Columns[i].NotNull = false
			}
		}
		if definitionHook != nil {
			if err := definitionHook(ctx, &definition); err != nil {
				return nil, err
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func fdwSQLImportTable(ctx context.Context, pool *fdwSQLPool, schema, table string) (stuntgres.ForeignTableDefinition, error) {
	definition := stuntgres.ForeignTableDefinition{Name: table, Options: []stuntgres.ForeignOption{{Name: "schema_name", Value: schema}, {Name: "table_name", Value: table}}}
	probe, err := pool.QueryContext(ctx, "SELECT * FROM "+fdwQuote(schema)+"."+fdwQuote(table)+" LIMIT 0")
	if err != nil {
		return definition, err
	}
	types, err := probe.ColumnTypes()
	if closeErr := probe.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return definition, err
	}
	rows, err := pool.QueryContext(ctx, "SELECT a.attname,a.atttypmod,a.attnotnull FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum", schema, table)
	if err != nil {
		return definition, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var modifier int32
		var notNull bool
		if err := rows.Scan(&name, &modifier, &notNull); err != nil {
			return definition, err
		}
		slot := len(definition.Columns)
		if slot >= len(types) || types[slot].Name() != name {
			return definition, fmt.Errorf("SQL source column metadata is inconsistent: %s.%s", table, name)
		}
		databaseType := types[slot].DatabaseTypeName()
		typ := strings.ToLower(databaseType)
		switch databaseType {
		case "INT2", "INT4", "INT8", "TEXT", "BOOL", "BYTEA", "FLOAT4", "FLOAT8", "NUMERIC", "VARCHAR", "BPCHAR", "DATE", "TIME", "TIMETZ", "TIMESTAMP", "TIMESTAMPTZ", "UUID", "JSON", "JSONB":
		default:
			return definition, fmt.Errorf("SQL reader cannot import source type %q", databaseType)
		}
		spelling := "pg_catalog." + typ
		if modifier >= 0 {
			switch typ {
			case "varchar", "bpchar":
				spelling += fmt.Sprintf("(%d)", modifier-4)
			case "numeric":
				m := modifier - 4
				spelling += fmt.Sprintf("(%d,%d)", (m>>16)&65535, int16(m&65535))
			case "timestamp", "timestamptz", "time", "timetz":
				spelling += fmt.Sprintf("(%d)", modifier)
			default:
				return definition, fmt.Errorf("SQL reader cannot represent source typmod %s/%d", typ, modifier)
			}
		}
		definition.Columns = append(definition.Columns, stuntgres.ForeignColumnDefinition{Name: name, Type: spelling, NotNull: notNull, Options: []stuntgres.ForeignOption{{Name: "column_name", Value: name}}})
	}
	if err := rows.Err(); err != nil {
		return definition, err
	}
	if len(definition.Columns) != len(types) {
		return definition, fmt.Errorf("SQL source column metadata is incomplete: %s", table)
	}
	return definition, nil
}

func fdwSourceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, reader *fdwSQLReader, run func(fixture.Step), query string, count int64) {
	t.Helper()
	if b.PG {
		run(fixture.Exec(query, count))
		return
	}
	result, err := reader.source.ExecContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatalf("source %s count=%d err=%v want=%d", query, got, err, count)
	}
}
