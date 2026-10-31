package core

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *textSearchHost) RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.scope().Op.Context()
	accumulator, err := sqltype.NewFTSRewriteRules(ctx, query, result)
	if err != nil {
		return sqltype.Value{}, err
	}
	rewrite := &textSearchRewrite{textSearchQuery: textSearchQuery{host: h, spi: h.nestedSQL(), text: text}, accumulator: accumulator}
	if err := h.textSearchSQL(text, false, rewrite); err != nil {
		return sqltype.Value{}, err
	}
	return rewrite.value, nil
}

func (h *textSearchHost) nestedSQL() exec.NestedSQL { return (*statementHost)(h) }

// One lexical nested-SQL owner is shared by rewrite and statistics consumers.
// Consumers close the cursor before scratch restoration; FETCH errors pass through.
func (h *textSearchHost) textSearchSQL(text string, readOnlySPI bool, consumer exec.NestedConsumer) error {
	spi := h.nestedSQL()
	if err := spi.EnterNestedSQL(); err != nil {
		return err
	}
	defer spi.LeaveNestedSQL()
	read, statements, err := spi.ParseNestedSQL(h.scope().Op.Context(), text)
	if err != nil {
		return exec.RewriteSQLDiagnostic(err, text)
	}
	if len(statements) != 1 {
		// SPI prepares every statement before checking cursor plan count.
		for _, statement := range statements {
			if err := spi.PrepareNestedSQL(statement, read); err != nil {
				return exec.RewriteSQLDiagnostic(err, text)
			}
		}
		return SQLError("42P11", "cannot open multi-query plan as cursor")
	}
	statement := statements[0]
	// SPI rejects non-cursor command kinds before its read-only plan check.
	// Tuple-producing stat queries use canonical executable binding admission;
	// rewrite retains its independently established syntax-gate contract.
	request := exec.NestedRequest{Text: text, Diagnose: true, Caller: "text search", ReadOnly: readOnlySPI && spi.NestedCursorStatement(statement)}
	return spi.ExecuteNestedSQL(statement, read, request, consumer)
}

// textSearchQuery is the cursor admission shared by ts_rewrite and ts_stat.
type textSearchQuery struct {
	host *textSearchHost
	spi  exec.NestedSQL
	text string
}

func (q *textSearchQuery) NestedCommand(parser.Statement) (bool, error) { return false, nil }

func (q *textSearchQuery) NestedAnalyzed(statement parser.Statement) error {
	if !q.spi.NestedCursorStatement(statement) {
		return SQLError("42P11", "cannot open "+q.spi.NestedCommandTag(statement)+" query as cursor")
	}
	if tag := q.spi.NestedReadOnlyViolation(statement); tag != "" {
		return exec.RewriteSQLDiagnostic(SQLError("0A000", tag+" is not allowed in a non-volatile function"), q.text)
	}
	return nil
}

func textSearchRows(rows exec.RowResult) error {
	if rows == nil {
		return fmt.Errorf("stuntgres: tuple-producing text search SQL returned no row cursor")
	}
	return nil
}

// textSearchRewrite consumes ts_rewrite's rule rows.
type textSearchRewrite struct {
	textSearchQuery
	accumulator *sqltype.FTSRewriteRules
	value       sqltype.Value
}

func (r *textSearchRewrite) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
	if err := textSearchRows(rows); err != nil {
		return err
	}
	r.value, err = exec.ConsumeRewriteRules(r.host.scope().Op.Context(), rows, r.accumulator)
	return err
}
