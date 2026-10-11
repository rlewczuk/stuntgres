package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func inheritanceQueryControlChecks() []fixture.Step {
	bag := fixture.Select("SELECT * FROM p", []string{"a", "x"}, []string{"INT4", "TEXT"}, inheritanceQueryFive())
	bag.Bag = true
	view := bag
	view.Query = "SELECT * FROM default_view"
	return []fixture.Step{
		bag, view,
		fixture.Select("SELECT * FROM ONLY p", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "same"}, {int64(7), "same"}}),
		fixture.Select("SELECT * FROM only_view", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "same"}, {int64(7), "same"}}),
		fixture.Select("SELECT * FROM only_alias", []string{"label"}, []string{"TEXT"}, [][]any{{"same"}, {"same"}}),
		fixture.Select("SELECT a,x FROM c ORDER BY z", []string{"a", "x"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "same"}, {int64(7), "same"}}),
	}
}

func inheritanceQueryControlRoundTrip(s *inheritanceCase) {
	checks := inheritanceQueryControlChecks()
	for _, check := range checks {
		s.run(check)
	}
	if s.b.PG {
		return
	} // Native controls are not a PostgreSQL server API.
	held, err := s.c.QueryContext(context.WithValue(s.p.Ctx, fixture.RecorderKey{}, s.record), checks[0].Query)
	if err != nil {
		s.t.Fatal(err)
	}
	defer held.Close()
	stmt, err := s.c.PrepareContext(s.p.Ctx, checks[0].Query)
	if err != nil {
		s.t.Fatal(err)
	}
	defer stmt.Close()
	var dump bytes.Buffer
	if err = stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err = stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.absent("p")
	if err = stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	for _, check := range checks {
		s.run(check)
	}
	fixture.RunStep(s.t, s.p.Ctx, fixture.SequenceFrozenRows{Executor: s.c, Rows: held, Query: checks[0].Query}, s.record, checks[0])
	fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: checks[0].Query}, s.record, checks[0])
	for _, suffix := range []string{"; CREATE TABLE broken() INHERITS(missing)", "; CREATE TABLE broken(a TEXT) INHERITS(p)"} {
		if err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
			s.t.Fatal("invalid restore succeeded")
		}
		for _, check := range checks {
			s.run(check)
		}
	}
	canceled, cancel := context.WithCancel(s.p.Ctx)
	cancel()
	if err = stuntgres.Restore(canceled, s.p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		s.t.Fatal(err)
	}
	for _, check := range checks {
		s.run(check)
	}
	if s.p.Postgres != nil {
		if _, err = s.p.Postgres.ExecContext(s.p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			s.t.Fatal(err)
		}
		if _, err = s.p.Postgres.ExecContext(s.p.Ctx, dump.String()); err != nil {
			s.t.Fatal("genuine native dump replay", err)
		}
		for _, check := range checks {
			check.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, s.p.Postgres, s.p.PostgresRecord, check)
		}
	}
}

func TestReferenceInheritanceQueryControls(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryDiamond(s)
		s.exec("CREATE VIEW default_view AS SELECT * FROM p", 0)
		s.exec("CREATE VIEW only_view AS SELECT * FROM ONLY p", 0)
		s.exec("CREATE VIEW only_alias AS SELECT r.x AS label FROM ONLY(p) r", 0)
		inheritanceQueryControlRoundTrip(s)
	})
}
