package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres"
)

// Optional write methods live on a separate wrapper, never promoted onto the
// deliberate scan-only provider. All mutations below execute real source SQL.
type fdwSQLWriter struct {
	*fdwSQLReader
	batchSize                                                                    int
	batches                                                                      []int
	modifies, modifyEnds, copyBegins, copyEnds, transactions, commits, rollbacks int
	statementBegins, statementEnds                                               int
	orderedScans                                                                 bool
	truncateGroups                                                               []int
	writeHook                                                                    func(context.Context, string) error
	rewrite                                                                      func(stuntgres.ForeignRow) (stuntgres.ForeignRow, bool)
	resultHook                                                                   func(stuntgres.ForeignRow) stuntgres.ForeignRow
	batchHook                                                                    func([]stuntgres.ForeignRow) []stuntgres.ForeignRow
	identityHook                                                                 func(stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity
	lateIsolation                                                                bool
	incrementRevision                                                            bool
}

func (w *fdwSQLWriter) writeCall(ctx context.Context, name string) error {
	if w.writeHook != nil {
		return w.writeHook(ctx, name)
	}
	return ctx.Err()
}
func (w *fdwSQLWriter) ForeignTransactionCapabilities(ctx context.Context, r stuntgres.ForeignRequest) (stuntgres.ForeignTransactionCapabilities, error) {
	return stuntgres.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, w.writeCall(ctx, "Capabilities")
}
func (w *fdwSQLWriter) BeginForeignTransaction(ctx context.Context, r stuntgres.ForeignRequest) (stuntgres.ForeignTransaction, error) {
	pool, err := w.pool(r.MappingOptions)
	if err != nil {
		return nil, err
	}
	// Owner completion, not the first statement's cancellation context, owns
	// the transaction lifetime. Every SQL operation still uses its current ctx.
	if r.Characteristics == nil {
		pool.Close()
		return nil, errors.New("SQL writer: missing owner transaction characteristics")
	}
	isolation := sql.LevelRepeatableRead
	switch r.Characteristics.Isolation {
	case "serializable":
		isolation = sql.LevelSerializable
	case "read committed", "read uncommitted":
		if w.lateIsolation {
			isolation = sql.LevelReadCommitted
		}
	case "repeatable read":
	default:
		pool.Close()
		return nil, fmt.Errorf("SQL writer: unsupported isolation %q", r.Characteristics.Isolation)
	}
	tx, err := pool.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{Isolation: isolation, ReadOnly: r.Characteristics.ReadOnly})
	if err != nil {
		pool.Close()
		return nil, err
	}
	if r.Characteristics.Deferrable {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION DEFERRABLE"); err != nil {
			tx.Rollback()
			pool.Close()
			return nil, err
		}
	}
	w.transactions++
	return &fdwWriterTransaction{Tx: tx, pool: pool, writer: w}, w.writeCall(ctx, "BeginTransaction")
}

type fdwWriterTransaction struct {
	*sql.Tx
	pool          *fdwSQLPool
	writer        *fdwSQLWriter
	statement     uint64
	statementName string
	closed        bool
}

func (t *fdwWriterTransaction) command(ctx context.Context, command string) error {
	_, err := t.Tx.ExecContext(ctx, command)
	return err
}
func (t *fdwWriterTransaction) BeginStatement(ctx context.Context) error {
	t.writer.statementBegins++
	t.statement++
	t.statementName = "writer_statement_" + strconv.FormatUint(t.statement, 10)
	return t.Savepoint(ctx, t.statementName)
}
func (t *fdwWriterTransaction) EndStatement(ctx context.Context, success bool) error {
	t.writer.statementEnds++
	if !success {
		if err := t.RollbackTo(ctx, t.statementName); err != nil {
			return err
		}
	}
	if err := t.Release(ctx, t.statementName); err != nil {
		return err
	}
	return t.writer.writeCall(ctx, "EndStatement")
}
func (t *fdwWriterTransaction) Savepoint(ctx context.Context, name string) error {
	return t.command(ctx, "SAVEPOINT "+fdwQuote(name))
}
func (t *fdwWriterTransaction) RollbackTo(ctx context.Context, name string) error {
	return t.command(ctx, "ROLLBACK TO "+fdwQuote(name))
}
func (t *fdwWriterTransaction) Release(ctx context.Context, name string) error {
	return t.command(ctx, "RELEASE "+fdwQuote(name))
}
func (t *fdwWriterTransaction) Commit(ctx context.Context) error {
	if err := t.writer.writeCall(ctx, "Commit"); err != nil {
		_ = t.Rollback(context.WithoutCancel(ctx))
		return err
	}
	t.writer.commits++
	t.closed = true
	err := t.Tx.Commit()
	if closeErr := t.pool.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = t.writer.writeCall(ctx, "AfterCommit")
	}
	return err
}
func (t *fdwWriterTransaction) Rollback(ctx context.Context) error {
	if t.closed {
		return nil
	}
	t.closed = true
	t.writer.rollbacks++
	err := t.Tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		err = nil
	}
	if closeErr := t.pool.Close(); err == nil {
		err = closeErr
	}
	return err
}
func fdwWriterTx(r stuntgres.ForeignRequest) (*fdwWriterTransaction, error) {
	tx, ok := r.Transaction.(*fdwWriterTransaction)
	if !ok {
		return nil, errors.New("SQL writer: no enlisted transaction")
	}
	return tx, nil
}
func (w *fdwSQLWriter) AddForeignUpdateTargets(ctx context.Context, r stuntgres.ForeignRequest, op stuntgres.ForeignModifyOperation) (stuntgres.ForeignUpdateTargets, error) {
	name := "id"
	if fdwOption(r.TableOptions, "table_name") == "identity_rows" {
		name = "rid"
	}
	return stuntgres.ForeignUpdateTargets{Names: []string{name}}, w.writeCall(ctx, "AddForeignUpdateTargets")
}
func (w *fdwSQLWriter) PlanForeignModify(ctx context.Context, r stuntgres.ForeignRequest, op stuntgres.ForeignModifyOperation, targets stuntgres.ForeignUpdateTargets) (stuntgres.ForeignModifyPlan, error) {
	return stuntgres.ForeignModifyPlan{Private: op}, w.writeCall(ctx, "PlanForeignModify")
}
func (w *fdwSQLWriter) BeginForeignModify(ctx context.Context, r stuntgres.ForeignModifyRequest) (any, error) {
	if r.Plan.Private != r.Operation {
		return nil, errors.New("SQL writer: wrong operation plan")
	}
	w.modifies++
	return r.Operation, w.writeCall(ctx, "BeginForeignModify")
}
func (w *fdwSQLWriter) EndForeignModify(ctx context.Context, r stuntgres.ForeignModifyRequest, state any) error {
	w.modifyEnds++
	return w.writeCall(ctx, "EndForeignModify")
}
func (w *fdwSQLWriter) BeginForeignInsert(ctx context.Context, r stuntgres.ForeignInsertRequest) (any, error) {
	if !r.Copy {
		return nil, errors.New("SQL writer: missing COPY origin")
	}
	w.copyBegins++
	return "copy", w.writeCall(ctx, "BeginForeignInsert")
}
func (w *fdwSQLWriter) EndForeignInsert(ctx context.Context, r stuntgres.ForeignInsertRequest, state any) error {
	w.copyEnds++
	return w.writeCall(ctx, "EndForeignInsert")
}
func fdwWriterColumns(r stuntgres.ForeignRequest) []string {
	columns := make([]string, len(r.Columns))
	for i, c := range r.Columns {
		name := fdwOption(c.Options, "column_name")
		if name == "" {
			name = c.Name
		}
		columns[i] = fdwQuote(name)
	}
	return columns
}
func fdwWriterValues(row stuntgres.ForeignRow) []any {
	args := make([]any, len(row))
	for i, v := range row {
		args[i] = v
	}
	return args
}
func fdwWriterReturning(ctx context.Context, tx *fdwWriterTransaction, query string, args []any, width int) (stuntgres.ForeignRow, bool, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	values := make([]any, width)
	dest := make([]any, width)
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, false, err
	}
	out := make(stuntgres.ForeignRow, width)
	for i, v := range values {
		if b, ok := v.([]byte); ok {
			out[i] = append([]byte(nil), b...)
		} else {
			out[i] = v
		}
	}
	if rows.Next() {
		return nil, false, errors.New("SQL writer: identity matched multiple rows")
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	return out, true, nil
}
func (w *fdwSQLWriter) returning(ctx context.Context, tx *fdwWriterTransaction, query string, args []any, width int) (stuntgres.ForeignRow, bool, error) {
	row, present, err := fdwWriterReturning(ctx, tx, query, args, width)
	if err == nil && present {
		err = w.writeCall(ctx, "AfterWrite")
	}
	if err == nil && present && w.resultHook != nil {
		row = w.resultHook(row)
	}
	return row, present, err
}
func (w *fdwSQLWriter) ExecForeignInsert(ctx context.Context, r stuntgres.ForeignModifyRequest, state any, row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	if err := w.writeCall(ctx, "ExecForeignInsert"); err != nil {
		return nil, false, err
	}
	if w.rewrite != nil {
		var present bool
		row, present = w.rewrite(row)
		if !present {
			return nil, false, nil
		}
	}
	tx, err := fdwWriterTx(r.ForeignRequest)
	if err != nil {
		return nil, false, err
	}
	columns := fdwWriterColumns(r.ForeignRequest)
	parameters := make([]string, len(columns))
	for i := range columns {
		parameters[i] = "$" + strconv.Itoa(i+1)
	}
	query := "INSERT INTO " + fdwRemote(r.ForeignRequest) + " (" + strings.Join(columns, ",") + ") VALUES(" + strings.Join(parameters, ",") + ") RETURNING " + strings.Join(columns, ",")
	return w.returning(ctx, tx, query, fdwWriterValues(row), len(columns))
}
func (w *fdwSQLWriter) GetForeignModifyBatchSize(ctx context.Context, r stuntgres.ForeignModifyRequest, state any) (int, error) {
	return w.batchSize, w.writeCall(ctx, "GetForeignModifyBatchSize")
}
func (w *fdwSQLWriter) ExecForeignBatchInsert(ctx context.Context, r stuntgres.ForeignModifyRequest, state any, rows []stuntgres.ForeignRow) ([]stuntgres.ForeignRow, error) {
	w.batches = append(w.batches, len(rows))
	if err := w.writeCall(ctx, "ExecForeignBatchInsert"); err != nil {
		return nil, err
	}
	var out []stuntgres.ForeignRow
	for _, row := range rows {
		actual, present, err := w.ExecForeignInsert(ctx, r, state, row)
		if err != nil {
			return nil, err
		}
		if present {
			out = append(out, actual)
		}
	}
	if w.batchHook != nil {
		out = w.batchHook(out)
	}
	return out, nil
}
func (w *fdwSQLWriter) ExecForeignUpdate(ctx context.Context, r stuntgres.ForeignModifyRequest, state any, identity stuntgres.ForeignRowIdentity, row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	if err := w.writeCall(ctx, "ExecForeignUpdate"); err != nil {
		return nil, false, err
	}
	if w.rewrite != nil {
		var present bool
		row, present = w.rewrite(row)
		if !present {
			return nil, false, nil
		}
	}
	tx, err := fdwWriterTx(r.ForeignRequest)
	if err != nil {
		return nil, false, err
	}
	columns := fdwWriterColumns(r.ForeignRequest)
	assignments := make([]string, len(columns))
	for i, c := range columns {
		assignments[i] = c + "=$" + strconv.Itoa(i+1)
	}
	if w.incrementRevision {
		assignments = append(assignments, "revision=revision+1")
	}
	args := append(fdwWriterValues(row), identity[0])
	return w.returning(ctx, tx, "UPDATE "+fdwRemote(r.ForeignRequest)+" SET "+strings.Join(assignments, ",")+" WHERE "+fdwQuote(r.Targets.Names[0])+"=$"+strconv.Itoa(len(args))+" RETURNING "+strings.Join(columns, ","), args, len(columns))
}
func (w *fdwSQLWriter) ExecForeignDelete(ctx context.Context, r stuntgres.ForeignModifyRequest, state any, identity stuntgres.ForeignRowIdentity, old stuntgres.ForeignRow) (stuntgres.ForeignRow, bool, error) {
	if err := w.writeCall(ctx, "ExecForeignDelete"); err != nil {
		return nil, false, err
	}
	tx, err := fdwWriterTx(r.ForeignRequest)
	if err != nil {
		return nil, false, err
	}
	columns := fdwWriterColumns(r.ForeignRequest)
	return w.returning(ctx, tx, "DELETE FROM "+fdwRemote(r.ForeignRequest)+" WHERE "+fdwQuote(r.Targets.Names[0])+"=$1 RETURNING "+strings.Join(columns, ","), []any{identity[0]}, len(columns))
}
func (w *fdwSQLWriter) ExecForeignTruncate(ctx context.Context, r stuntgres.ForeignTruncateRequest) error {
	if err := w.writeCall(ctx, "ExecForeignTruncate"); err != nil {
		return err
	}
	w.truncateGroups = append(w.truncateGroups, len(r.Tables))
	tables := make([]string, len(r.Tables))
	for i, t := range r.Tables {
		tables[i] = fdwRemote(t)
	}
	tx, ok := r.Transaction.(*fdwWriterTransaction)
	if !ok {
		return errors.New("SQL writer: truncate lacks transaction")
	}
	q := "TRUNCATE " + strings.Join(tables, ",")
	if r.RestartIdentity {
		q += " RESTART IDENTITY"
	} else {
		q += " CONTINUE IDENTITY"
	}
	if r.Cascade {
		q += " CASCADE"
	} else {
		q += " RESTRICT"
	}
	_, err := tx.ExecContext(ctx, q)
	return err
}

// Scans borrow the enlisted transaction. End closes scan resources only; the
// sole engine owner subsequently decides statement and transaction completion.
func (w *fdwSQLWriter) BeginForeignScan(ctx context.Context, r stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	tx, err := fdwWriterTx(r)
	if err != nil {
		return nil, err
	}
	query, visible := fdwWriterScanSQL(r, plan, w.orderedScans)
	w.begins++
	return &fdwWriterScan{writer: w, tx: tx, query: query, visible: visible, identity: r.Identity != nil}, w.writeCall(ctx, "BeginForeignScan")
}

// Shared by scan startup and planning-only row-lock explanation.
func fdwWriterScanSQL(r stuntgres.ForeignRequest, plan stuntgres.ForeignPlan, ordered bool) (string, int) {
	columns := fdwWriterColumns(r)
	var projection []string
	for _, a := range plan.Attributes {
		for i, c := range r.Columns {
			if c.Attribute == a { projection = append(projection, columns[i]); break }
		}
	}
	visible := len(projection)
	if r.Identity != nil {
		for _, name := range r.Identity.Names { projection = append(projection, fdwQuote(name)) }
	}
	if len(projection) == 0 { projection = append(projection, "1") }
	query := "SELECT " + strings.Join(projection, ",") + " FROM " + fdwRemote(r)
	if ordered { query += " ORDER BY id" }
	return query, visible
}

type fdwWriterScan struct {
	writer   *fdwSQLWriter
	tx       *fdwWriterTransaction
	query    string
	visible  int
	identity bool
	rows     []stuntgres.ForeignRow
	loaded   bool
	index    int
	ended    bool
}

func (s *fdwWriterScan) load(ctx context.Context) error {
	rows, err := s.tx.QueryContext(ctx, s.query)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		row := make(stuntgres.ForeignRow, len(values))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				row[i] = append([]byte(nil), bytes...)
			} else {
				row[i] = value
			}
		}
		s.rows = append(s.rows, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.loaded = true
	return rows.Close()
}
func (s *fdwWriterScan) IterateForeignIdentity(ctx context.Context) (stuntgres.ForeignRow, stuntgres.ForeignRowIdentity, bool, error) {
	if err := s.writer.writeCall(ctx, "IterateForeignScan"); err != nil {
		return nil, nil, false, err
	}
	if !s.loaded {
		if err := s.load(ctx); err != nil {
			return nil, nil, false, err
		}
	}
	if s.index == len(s.rows) {
		return nil, nil, false, nil
	}
	row := s.rows[s.index]
	s.index++
	var identity stuntgres.ForeignRowIdentity
	if s.identity {
		identity = append(stuntgres.ForeignRowIdentity(nil), row[s.visible:]...)
	}
	if s.identity && s.writer.identityHook != nil {
		identity = s.writer.identityHook(identity)
	}
	return row[:s.visible], identity, true, nil
}
func (s *fdwWriterScan) IterateForeignScan(ctx context.Context) (stuntgres.ForeignRow, bool, error) {
	row, _, present, err := s.IterateForeignIdentity(ctx)
	return row, present, err
}
func (s *fdwWriterScan) ReScanForeignScan(ctx context.Context, p stuntgres.ForeignScanParameters) error {
	s.rows = nil
	s.index = 0
	s.loaded = false
	return s.writer.writeCall(ctx, "ReScanForeignScan")
}
func (s *fdwWriterScan) EndForeignScan(ctx context.Context) error {
	if s.ended {
		return fmt.Errorf("SQL writer: duplicate scan End")
	}
	s.ended = true
	s.writer.ends++
	s.rows = nil
	return s.writer.writeCall(ctx, "EndForeignScan")
}

var _ stuntgres.ForeignDataWrapper = (*fdwSQLWriter)(nil)
