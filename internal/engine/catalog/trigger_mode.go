package catalog

// TriggerMode is the persistent pg_trigger.tgenabled/pg_event_trigger.evtenabled value.
type TriggerMode byte

const (
	TriggerOrigin   TriggerMode = 'O'
	TriggerDisabled TriggerMode = 'D'
	TriggerReplica  TriggerMode = 'R'
	TriggerAlways   TriggerMode = 'A'
)

// Normalized permits old definition literals to mean the CREATE default.
func (m TriggerMode) Normalized() TriggerMode {
	if m == 0 {
		return TriggerOrigin
	}
	return m
}
func (m TriggerMode) Valid() bool {
	return m == TriggerOrigin || m == TriggerDisabled || m == TriggerReplica || m == TriggerAlways
}
func (m TriggerMode) Fires(replica bool) bool {
	m = m.Normalized()
	return m == TriggerAlways || m == TriggerOrigin && !replica || m == TriggerReplica && replica
}
func (m TriggerMode) String() string {
	m = m.Normalized()
	if !m.Valid() {
		return ""
	}
	return string(byte(m))
}
