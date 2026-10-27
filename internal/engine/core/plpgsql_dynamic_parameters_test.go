package core_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLDynamicSQLParameterIsolation(t *testing.T) {
	for _, set := range []bool{false, true} {
		for _, outerClosed := range []bool{false, true} {
			t.Run(fmt.Sprintf("set_%t_outer_closed_%t", set, outerClosed), func(t *testing.T) {
				s, d, _, read := loopUnitFrame(t, "BEGIN RETURN 7; END")
				source := "DECLARE x integer:=99; BEGIN EXECUTE 'SELECT $1::integer' INTO x; EXECUTE 'SELECT $2::integer' INTO x USING 1; EXECUTE 'SELECT $1::integer' INTO x USING 7; RETURN 7; END"
				action := "at EXECUTE"
				if set {
					source = "BEGIN RETURN QUERY EXECUTE 'SELECT $1::integer'; RETURN QUERY EXECUTE 'SELECT $2::integer' USING 1; RETURN QUERY EXECUTE 'SELECT $1::integer' USING 7; END"
					action = "at RETURN QUERY"
				}
				raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				d.SQL.Source, d.SQL.PLSyntax, d.SQL.ReturnsSet = source, raw, set
				s.Engine().Mu.Lock()
				auth, err := s.Engine().AuthorizationLocked(s.Op)
				if err == nil {
					err = s.Engine().CompilePLRoutineLocked(s.Op, auth, d)
				}
				s.Engine().Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				input := proc.NewFrame(nil)
				defer input.Close()
				frame := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
				defer frame.Close()
				body := d.SQL.PL.Root.Statements[0].Block
				frame.Enter(d.SQL.PL.Root)
				defer frame.Leave(d.SQL.PL.Root)
				frame.Enter(body)
				defer frame.Leave(body)
				integer, _ := read.LookupType(sqltype.Int4)
				old, _ := sqltype.Integer(integer, 99)
				if !set {
					frame.Values[body.Bindings["x"]] = old
				}
				run := core.ScopeNewPLRun(s, d, frame, read)
				var sink []sqltype.Value
				if set {
					run.Values = &sink
				}
				core.SetRoutineDemandClosedParameters(core.ScopeSQLRoutineRef(s), outerClosed)
				before, expected, depth := core.ScopeSQLRoutine(s), s.Expected, core.ScopeActiveSQLRoutineDepth(s)
				for _, index := range []int{0, 1, 2, 0, 2} {
					var targetBefore sqltype.Value
					if !set {
						targetBefore = frame.Values[body.Bindings["x"]]
					}
					rowsBefore := len(sink)
					foundBefore := frame.Values[frame.Program.FoundSlot]
					err := run.Host.Dynamic(run, frame, body.Statements[index])
					if index < 2 {
						var diagnostic *sqlstate.Error
						parameter := fmt.Sprintf("$%d", index+1)
						if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" || diagnostic.Message != "there is no parameter "+parameter || diagnostic.Position != "" || diagnostic.InternalPosition != "8" || diagnostic.InternalQuery != "SELECT "+parameter+"::integer" || diagnostic.Context != "PL/pgSQL function loop_unit() line 1 "+action {
							t.Fatal("missing parameter escaped computed binding diagnostic", diagnostic, err)
						}
					} else if err != nil {
						t.Fatal("same-frame valid USING recovery", err)
					}
					if s.Expected != expected || core.ScopeActiveSQLRoutineDepth(s) != depth || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), before) || core.RoutineDemandClosedParameters(core.ScopeSQLRoutineRef(s)) != outerClosed || d.SQL.PL.ValidateDefinition(d.SQL) != nil {
						t.Fatal("dynamic parameter boundary escaped its activation")
					}
					if index < 2 && (len(sink) != rowsBefore || !set && frame.Values[body.Bindings["x"]] != targetBefore || frame.Values[frame.Program.FoundSlot] != foundBefore) {
						t.Fatal("failed computed binding published a target or set row")
					}
				}
				if set {
					if len(sink) != 2 {
						t.Fatal("failed bind appended to set sink", len(sink))
					}
					for _, value := range sink {
						if n, err := value.Integer(); err != nil || n != 7 {
							t.Fatal("set USING recovery value", value, err)
						}
					}
				}
				// A nested ordinary activation must start open, even inside a
				// closed dynamic owner, and restore the exact previous demand.
				restore := s.ActivateSQLRoutine(nil, read)
				if core.RoutineDemandClosedParameters(core.ScopeSQLRoutineRef(s)) {
					t.Fatal("ordinary activation inherited closed USING namespace")
				}
				description, err := s.Describe(enginetest.Statement(t, "SELECT $1::integer"))
				err = restore(err)
				if err != nil || description.Parameters.Count != 1 || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), before) {
					t.Fatal("dynamic boundary changed ordinary public inference or restoration", description.Parameters, err)
				}
			})
		}
	}
}
