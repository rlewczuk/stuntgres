package fixture

func MaterializedViewUnpopulated(name string) Step {
	want := ControlsReferenceDiagnostic("55000", `materialized view "`+name+`" has not been populated`)
	want.Hint = "Use the REFRESH MATERIALIZED VIEW command."
	s := ErrorDiagnostic("SELECT * FROM "+name, want)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func MaterializedViewPopulation(run func(Step), name string, populated bool) {
	run(Select("SELECT relkind='m' AS m,relhasrules,relispopulated FROM pg_class WHERE relname='"+name+"'", []string{"m", "relhasrules", "relispopulated"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, populated}}))
}
