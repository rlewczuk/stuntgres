package control

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
)

// Constructors do not inherit statistics settings. ONLY prevents replaying a
// parent's explicit target from changing a child's independent configuration.
func emitColumnStatistics(w *dumpWriter, qualified, name string, value catalog.ColumnStatisticsConfig) error {
	prefix := "ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(name)
	if value.TargetSet {
		if err := w.emit(fmt.Sprintf("%s SET STATISTICS %d;\n", prefix, value.Target)); err != nil {
			return err
		}
	}
	if len(value.Options) != 0 {
		options := make([]string, len(value.Options))
		for i, option := range value.Options {
			options[i] = catalog.QuoteIdentifier(option.Name) + "=" + dump.CommentLiteral(option.Value)
		}
		return w.emit(prefix + " SET (" + strings.Join(options, ",") + ");\n")
	}
	return nil
}
