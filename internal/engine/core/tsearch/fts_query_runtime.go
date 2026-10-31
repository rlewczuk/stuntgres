package tsearch

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Rewrite is ts_rewrite with its rules from text's query.
func Rewrite[A Authority[A]](h Host[A], query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.Operation().Context()
	accumulator, err := sqltype.NewFTSRewriteRules(ctx, query, result)
	if err != nil {
		return sqltype.Value{}, err
	}
	rewrite := &textSearchRewrite[A]{textSearchQuery: textSearchQuery[A]{host: h, text: text}, accumulator: accumulator}
	if err := textSearchSQL(h, text, false, rewrite); err != nil {
		return sqltype.Value{}, err
	}
	return rewrite.value, nil
}

// One lexical nested-SQL owner is shared by rewrite and statistics consumers.
// Consumers close the cursor before scratch restoration; FETCH errors pass through.
func textSearchSQL[A Authority[A]](spi Host[A], text string, readOnlySPI bool, consumer exec.NestedConsumer) error {
	if err := spi.EnterNestedSQL(); err != nil {
		return err
	}
	defer spi.LeaveNestedSQL()
	read, statements, err := spi.ParseNestedSQL(spi.Operation().Context(), text)
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
		return sqlError("42P11", "cannot open multi-query plan as cursor")
	}
	statement := statements[0]
	// SPI rejects non-cursor command kinds before its read-only plan check.
	// Tuple-producing stat queries use canonical executable binding admission;
	// rewrite retains its independently established syntax-gate contract.
	request := exec.NestedRequest{Text: text, Diagnose: true, Caller: "text search", ReadOnly: readOnlySPI && spi.NestedCursorStatement(statement)}
	return spi.ExecuteNestedSQL(statement, read, request, consumer)
}

// textSearchQuery is the cursor admission shared by ts_rewrite and ts_stat.
type textSearchQuery[A Authority[A]] struct {
	host Host[A]
	text string
}

func (q *textSearchQuery[A]) NestedCommand(parser.Statement) (bool, error) { return false, nil }

func (q *textSearchQuery[A]) NestedAnalyzed(statement parser.Statement) error {
	if !q.host.NestedCursorStatement(statement) {
		return sqlError("42P11", "cannot open "+q.host.NestedCommandTag(statement)+" query as cursor")
	}
	if tag := q.host.NestedReadOnlyViolation(statement); tag != "" {
		return exec.RewriteSQLDiagnostic(sqlError("0A000", tag+" is not allowed in a non-volatile function"), q.text)
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
type textSearchRewrite[A Authority[A]] struct {
	textSearchQuery[A]
	accumulator *sqltype.FTSRewriteRules
	value       sqltype.Value
}

func (r *textSearchRewrite[A]) NestedRows(_ parser.Statement, rows exec.RowResult) (err error) {
	if err := textSearchRows(rows); err != nil {
		return err
	}
	r.value, err = exec.ConsumeRewriteRules(r.host.Operation().Context(), rows, r.accumulator)
	return err
}
