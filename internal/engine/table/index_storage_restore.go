package table

import (
	"math"
	"strings"
	"unicode/utf8"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

const RestoreIndexStorageBoundary = "restore accepts only canonical pg_attribute index storage repairs"

// RestoredIndexStorage is a bounded index-attribute repair, not writable
// catalog state. Names can be staged separately to break rename cycles.
type RestoredIndexStorage struct {
	catalog.ColumnStorage
	Name string
	Rename bool
	SetStorage bool
}

func RestoreIndexStorageSyntax(raw *parser.Update) (parser.ObjectName, int32, RestoredIndexStorage, error) {
	reject := func() (parser.ObjectName, int32, RestoredIndexStorage, error) { return parser.ObjectName{}, 0, RestoredIndexStorage{}, sqlError("0A000", RestoreIndexStorageBoundary) }
	if len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "pg_catalog" || raw.Name.Parts[1].Text != "pg_attribute" || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) < 1 || len(raw.Assignments) > 3 { return reject() }
	value := RestoredIndexStorage{}
	assignments := raw.Assignments
	if assignments[len(assignments)-1].Target.Text == "attname" {
		assignment := assignments[len(assignments)-1]
		literal, ok := assignment.Value.(parser.LiteralExpr)
		if len(assignment.Steps) != 0 || !ok || literal.Value.Kind != parser.StringValue { return reject() }
		value.Name = literal.Value.Text
		if value.Name == "" || len(value.Name) > 63 || !utf8.ValidString(value.Name) || strings.IndexByte(value.Name, 0) >= 0 { return reject() }
		value.Rename = true
		assignments = assignments[:len(assignments)-1]
	}
	if len(assignments) != 0 && len(assignments) != 2 { return reject() }
	value.SetStorage = len(assignments) == 2
	for i, assignment := range assignments {
		column := []string{"attstorage", "attcompression"}[i]
		literal, ok := assignment.Value.(parser.LiteralExpr)
		if assignment.Target.Text != column || len(assignment.Steps) != 0 || !ok || literal.Value.Kind != parser.StringValue { return reject() }
		text := literal.Value.Text
		if i == 0 {
			if text != "p" && text != "e" && text != "x" && text != "m" { return reject() }
			value.Storage = text
		} else {
			if text != "" && text != "p" && text != "l" { return reject() }
			value.Compression = text
		}
	}
	where, ok := raw.Where.(parser.LogicalExpr)
	if !ok || where.Op != parser.LogicalAnd { return reject() }
	relation, ok := restoreAttributeRankEquality(where.Left, "attrelid")
	if !ok { return reject() }
	cast, ok := relation.(parser.RegclassExpr)
	if !ok { return reject() }
	text, ok := cast.Child.(parser.LiteralExpr)
	if !ok || text.Value.Kind != parser.StringValue { return reject() }
	name, err := parser.ParseRegclassName(text.Value.Text)
	if err != nil || len(name.Parts) != 2 { return reject() }
	number, ok := restoreAttributeRankEquality(where.Right, "attnum")
	if !ok { return reject() }
	literal, ok := number.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.IntegerValue || literal.Value.Integer <= 0 || literal.Value.Integer > math.MaxInt32 { return reject() }
	return name, int32(literal.Value.Integer), value, nil
}
