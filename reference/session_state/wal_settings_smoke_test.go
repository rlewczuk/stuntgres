package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALSettingsSimulationSmoke(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		for _, name := range []string{"archive_command","wal_buffers","checkpoint_flush_after","wal_summary_keep_time"} {
			d := walDefinition(name); s.walValue(name,d.show,d.raw,walSource(d,s.pg),true)
		}
		peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
		s.run(fixture.Exec("CREATE TABLE public.wal_settings_sentinel(id integer NOT NULL)",0))
		s.run(fixture.Exec("INSERT INTO public.wal_settings_sentinel VALUES(7)",1))
		for _, m := range walMutable {
			s.walSet(m.name,m.input,false); s.walValue(m.name,m.show,m.raw,"session",true)
			d := walDefinition(m.name); peer.walValue(m.name,d.show,d.raw,"default",true)
		}
		s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("INSERT INTO public.wal_settings_sentinel VALUES(8)",1)); s.run(fixture.Exec("ROLLBACK",0))
		data := func(rows [][]any) { s.run(fixture.Select("SELECT id FROM public.wal_settings_sentinel ORDER BY id",[]string{"id"},[]string{"INT4"},rows)) }
		data([][]any{{int64(7)}})
		s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("INSERT INTO public.wal_settings_sentinel VALUES(9)",1)); s.run(fixture.Exec("COMMIT",0))
		data([][]any{{int64(7)},{int64(9)}})
		diagnostic := physicalDiagnostic("23502",`null value in column "id" of relation "wal_settings_sentinel" violates not-null constraint`)
		diagnostic.Detail = "Failing row contains (null)."; diagnostic.Schema = "public"; diagnostic.Table = "wal_settings_sentinel"; diagnostic.Column = "id"
		s.run(physicalError("INSERT INTO public.wal_settings_sentinel VALUES(NULL)",diagnostic,false,false))
		data([][]any{{int64(7)},{int64(9)}})
		s.run(physicalError("SET fsync=off",walContext(walDefinition("fsync")),false,false))
		s.run(physicalError("SET wal_level=logical",physicalRestart("wal_level"),true,false))
		data([][]any{{int64(7)},{int64(9)}})
		s.run(fixture.Exec("RESET ALL",0)); s.walDefaults(true)
		data([][]any{{int64(7)},{int64(9)}})
	})
}
