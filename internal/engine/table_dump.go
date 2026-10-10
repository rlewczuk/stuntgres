package engine

import (
	"context"
	"strings"
	"stuntgres/internal/parser"
)

// Immutable table metadata and row frontier captured by Dump under Engine.mu.
type dumpRelation struct {
	owner, schema, name string
	columns             []ColumnDescriptor
	attributes          []int32
	versions            []rowVersion
	ends                *deletionRoot
	frontier            CommitSeq
	comment             string
	columnComments      map[int32]string
	notNullConstraints  []notNullConstraint
	defaults            map[int32]columnDefault
	descriptor          *relationDescriptor
	identities          map[int32]columnIdentity
	identitySequences   map[ObjectAddress]dumpSequence
}

func (t *dumpRelation) emitDeferredDefaults(ctx context.Context, catalog CatalogRead, emit func(string) error) error {
	qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
	for i, c := range t.columns {
		definition, exists := t.defaults[t.attributes[i]]
		if !exists || !defaultReplayDeferrable(definition) {
			continue
		}
		text, err := defaultSQL(ctx, catalog, definition.expression)
		if err != nil {
			return err
		}
		if err := emit("ALTER TABLE " + qualified + " ALTER COLUMN " + quoteIdentifier(c.Name) + " SET DEFAULT " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func (t *dumpRelation) emitRows(ctx context.Context, catalog CatalogRead, emit func(string) error) error {
	qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
	var line strings.Builder
	for _, version := range t.versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !liveVersion(version, t.frontier, t.ends, nil) {
			continue
		}
		row := version.values
		line.Reset()
		if len(t.columns) == 0 {
			if err := emit("INSERT INTO " + qualified + " DEFAULT VALUES;\n"); err != nil {
				return err
			}
			continue
		}
		line.WriteString("INSERT INTO " + qualified)
		if len(t.identities) != 0 {
			line.WriteString(" OVERRIDING SYSTEM VALUE")
		}
		line.WriteString(" VALUES (")
		for i, v := range row {
			if i > 0 {
				line.WriteString(", ")
			}
			if _, generated := t.descriptor.generations[t.attributes[i]]; generated {
				line.WriteString("DEFAULT")
				continue
			}
			literal, err := catalogLiteralSQL(ctx, catalog, literalExpression{expressionInfo: expressionInfo{usage: t.columns[i].Usage}, value: v})
			if err != nil {
				return err
			}
			line.WriteString(literal)
		}
		line.WriteString(");\n")
		if err := emit(line.String()); err != nil {
			return err
		}
	}
	return nil
}

// Replay uses the ordinary binder/publication path, then admits only the same
// captured-reference default definitions that Dump can defer for a legal cycle.
func (e *Engine) validateRestoredDefault(op *Operation, s *parser.AlterTableDefault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveReference(auth, s.Name)
	if err != nil {
		return err
	}
	d := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	slot, ok := d.index[s.Column.Text]
	if !ok || !defaultReplayDeferrable(d.defaults[d.attributes[slot]]) {
		return sqlError("0A000", "restore accepts only deferred captured-reference defaults")
	}
	return nil
}
