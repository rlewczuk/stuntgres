package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func infoCharacterDataRejectInputs(t *testing.T, p *fixture.Pair, pg bool, conn fixture.Executor, recorder *fixture.Recorder, want [][]any, inputs []string) {
	t.Helper()
	const query = "INSERT INTO public.info_character_data_witness VALUES (90,'prefix'),(91,$1)"
	stmt, err := conn.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			t.Error(err)
		}
	}()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
	for i, input := range inputs {
		bytes := "0x00"
		if input == string([]byte{0xff}) {
			bytes = "0xff"
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("input=%d/held=%v", i, prepared), func(t *testing.T) {
				s := fixture.ImplicitError(query, fixture.Diagnostic{
					Code:    "22021",
					Message: `invalid byte sequence for encoding "UTF8": ` + bytes,
					Context: "unnamed portal parameter $1",
				}, input)
				s.ErrorStage = "execute"
				var executor fixture.Executor = conn
				if prepared {
					executor = held
				}
				fixture.RunStep(t, p.Ctx, executor, recorder, s)
				// Parameter validation may precede execution; neither row may publish.
				infoCharacterDataRead(t, p, pg, conn, recorder, want)
			})
		}
	}
}

func TestReferenceInfoCharacterDataDiagnosticsAtomicity(t *testing.T) {
	infoCharacterDataScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_character_data_witness VALUES (1,'seed'),(2,NULL)", 2))
		want := [][]any{{int64(1), "seed"}, {int64(2), nil}}
		infoCharacterDataRejectInputs(t, p, backend.PG, conn, recorder, want, []string{"a\x00b", string([]byte{0xff})})
	})
}
