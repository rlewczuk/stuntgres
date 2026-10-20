package session

// Notification carries a committed local event. PID is the full logical session
// identity, not an operating-system process ID.
type Notification struct {
	PID              uint64
	Channel, Payload string
}

type NotificationAction struct {
	Channel     string
	Listen, All bool
}
