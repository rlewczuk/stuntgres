package parser

import (
	"strings"
	"testing"
)

// SELECTED_FUNC adds a following-token possibility to a bare cursor motion.
// The selected reduction must still enter the count expression unless the
// explicit FROM/IN separator selects the omitted-count branch.
func TestPLpgSQLIntegerForConflictResolution(t *testing.T) {
	for _, command := range []string{"FETCH", "MOVE"} {
		for _, direction := range []string{"FORWARD", "BACKWARD"} {
			for _, counted := range []bool{false, true} {
				separators := []string{"FROM"}
				if !counted {
					separators = append(separators, "IN")
				}
				for _, separator := range separators {
					name := command + "_" + direction + "_" + separator
					count, cursor := "", `"reverse"`
					if counted {
						name += "_expression"
						count = "reverse('1')::integer "
						cursor = "c"
					} else {
						name += "_omitted"
					}
					t.Run(name, func(t *testing.T) {
						target := ""
						if command == "FETCH" {
							target = " INTO x"
						}
						source := "BEGIN " + command + " " + direction + " " + count + separator + " " + cursor + target + "; END"
						block, err := ParsePLpgSQL(source, PLReturnPolicy{})
						if err != nil {
							t.Fatal(err)
						}
						in := block.Statements[0].Cursor
						if in == nil {
							t.Fatal("lost cursor statement")
						}
						motion := in.Motion
						wantDirection := CursorForward
						if direction == "BACKWARD" {
							wantDirection = CursorBackward
						}
						if motion.Direction != wantDirection || motion.Counted != counted || motion.All {
							t.Fatal("motion branch", motion)
						}
						variable, ok := in.Variable.(ColumnExpr)
						wantCursor := "reverse"
						if counted {
							wantCursor = "c"
						}
						if !ok || len(variable.Name.Parts) != 1 || variable.Name.Parts[0].Text != wantCursor {
							t.Fatal("cursor consumed into count", in.Variable)
						}
						if !counted {
							if motion.Count != nil || motion.CountStart != (Position{}) || motion.CountEnd != (Position{}) || motion.CountDelimiter != (Position{}) {
								t.Fatal("omitted count payload", motion)
							}
							return
						}
						cast, ok := motion.Count.(CastExpr)
						if !ok {
							t.Fatalf("count is %T, want selected-call cast", motion.Count)
						}
						call, ok := cast.Child.(FunctionExpr)
						if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != "reverse" || len(call.Args) != 1 {
							t.Fatal("selected call count", cast.Child)
						}
						if got := PLExpressionSource(source, motion.CountStart, motion.CountEnd); got != "reverse('1')::integer" {
							t.Fatal("count source", got)
						}
						wantDelimiter := strings.Index(source, " "+separator+" ") + 1
						if motion.CountDelimiter.Offset != wantDelimiter || motion.CountDelimiter.Character != wantDelimiter+1 {
							t.Fatal("count delimiter", motion.CountDelimiter, wantDelimiter)
						}
					})
				}
			}
		}
	}
}
