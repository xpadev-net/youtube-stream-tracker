package ids

import (
	"github.com/google/uuid"
)

const (
	// MonitorPrefix is the prefix for monitor IDs.
	MonitorPrefix = "mon-"
)

// NewMonitorID generates a new monitor ID using UUIDv7.
// Format: mon-<uuidv7>
// UUIDv7 is time-ordered, making IDs sortable by creation time.
func NewMonitorID() string {
	return MonitorPrefix + uuid.Must(uuid.NewV7()).String()
}

// IsValidMonitorID checks if a string is a valid monitor ID.
func IsValidMonitorID(id string) bool {
	if len(id) < len(MonitorPrefix) {
		return false
	}
	if id[:len(MonitorPrefix)] != MonitorPrefix {
		return false
	}
	uuidPart := id[len(MonitorPrefix):]
	_, err := uuid.Parse(uuidPart)
	return err == nil
}
