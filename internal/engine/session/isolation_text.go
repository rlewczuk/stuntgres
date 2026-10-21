package session

// IsolationFromText maps a transaction_isolation setting value to its level;
// any other text is read committed.
func IsolationFromText(text string) Isolation {
	switch text {
	case "read uncommitted":
		return ReadUncommitted
	case "repeatable read":
		return RepeatableRead
	case "serializable":
		return Serializable
	default:
		return ReadCommitted
	}
}

// IsolationText is the setting text of level; the default level reads as read
// committed.
func IsolationText(level Isolation) string {
	switch level {
	case ReadUncommitted:
		return "read uncommitted"
	case RepeatableRead:
		return "repeatable read"
	case Serializable:
		return "serializable"
	default:
		return "read committed"
	}
}
