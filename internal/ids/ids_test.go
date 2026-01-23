package ids

import (
	"strings"
	"testing"
)

func TestNewMonitorID(t *testing.T) {
	id := NewMonitorID()

	// Check prefix
	if !strings.HasPrefix(id, MonitorPrefix) {
		t.Errorf("NewMonitorID() = %v, want prefix %v", id, MonitorPrefix)
	}

	// Check length (mon- + UUID with hyphens = 4 + 36 = 40)
	if len(id) != 40 {
		t.Errorf("NewMonitorID() length = %v, want 40", len(id))
	}

	// Check it's valid
	if !IsValidMonitorID(id) {
		t.Errorf("NewMonitorID() = %v, should be valid", id)
	}
}

func TestNewMonitorID_Unique(t *testing.T) {
	id1 := NewMonitorID()
	id2 := NewMonitorID()

	if id1 == id2 {
		t.Errorf("NewMonitorID() generated duplicate IDs: %v", id1)
	}
}

func TestIsValidMonitorID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{
			name: "valid monitor ID",
			id:   "mon-0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d",
			want: true,
		},
		{
			name: "valid monitor ID with different UUID",
			id:   "mon-12345678-1234-1234-1234-123456789abc",
			want: true,
		},
		{
			name: "missing prefix",
			id:   "0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d",
			want: false,
		},
		{
			name: "wrong prefix",
			id:   "monitor-0190a5c8-e4b0-7d8a-9c1d-2e3f4a5b6c7d",
			want: false,
		},
		{
			name: "invalid UUID format",
			id:   "mon-invalid-uuid",
			want: false,
		},
		{
			name: "empty string",
			id:   "",
			want: false,
		},
		{
			name: "too short",
			id:   "mon-",
			want: false,
		},
		{
			name: "UUID without hyphens (also valid)",
			id:   "mon-0190a5c8e4b07d8a9c1d2e3f4a5b6c7d",
			want: true, // UUID.Parse accepts both formats
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidMonitorID(tt.id); got != tt.want {
				t.Errorf("IsValidMonitorID(%v) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
