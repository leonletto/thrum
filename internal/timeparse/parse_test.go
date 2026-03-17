package timeparse_test

import (
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/timeparse"
)

func TestParseBefore(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name      string
		input     string
		wantErr   bool
		checkTime func(t *testing.T, got time.Time)
	}{
		{
			name:  "relative 2d",
			input: "2d",
			checkTime: func(t *testing.T, got time.Time) {
				expected := now.Add(-48 * time.Hour)
				delta := got.Sub(expected)
				if delta < -2*time.Second || delta > 2*time.Second {
					t.Errorf("2d: got %v, want ~%v (delta %v)", got, expected, delta)
				}
			},
		},
		{
			name:  "relative 7d",
			input: "7d",
			checkTime: func(t *testing.T, got time.Time) {
				expected := now.Add(-7 * 24 * time.Hour)
				delta := got.Sub(expected)
				if delta < -2*time.Second || delta > 2*time.Second {
					t.Errorf("7d: got %v, want ~%v (delta %v)", got, expected, delta)
				}
			},
		},
		{
			name:  "go duration 24h",
			input: "24h",
			checkTime: func(t *testing.T, got time.Time) {
				expected := now.Add(-24 * time.Hour)
				delta := got.Sub(expected)
				if delta < -2*time.Second || delta > 2*time.Second {
					t.Errorf("24h: got %v, want ~%v (delta %v)", got, expected, delta)
				}
			},
		},
		{
			name:  "go duration 2h30m",
			input: "2h30m",
			checkTime: func(t *testing.T, got time.Time) {
				expected := now.Add(-(2*time.Hour + 30*time.Minute))
				delta := got.Sub(expected)
				if delta < -2*time.Second || delta > 2*time.Second {
					t.Errorf("2h30m: got %v, want ~%v (delta %v)", got, expected, delta)
				}
			},
		},
		{
			name:  "date only",
			input: "2026-03-15",
			checkTime: func(t *testing.T, got time.Time) {
				expected := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
				if !got.Equal(expected) {
					t.Errorf("2026-03-15: got %v, want %v", got, expected)
				}
			},
		},
		{
			name:  "full RFC3339",
			input: "2026-03-15T14:30:00Z",
			checkTime: func(t *testing.T, got time.Time) {
				expected := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC)
				if !got.Equal(expected) {
					t.Errorf("RFC3339: got %v, want %v", got, expected)
				}
			},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "notadate",
			wantErr: true,
		},
		{
			name:    "negative days",
			input:   "-2d",
			wantErr: true,
		},
		{
			name:    "zero days",
			input:   "0d",
			wantErr: true,
		},
		{
			name:    "negative duration",
			input:   "-24h",
			wantErr: true,
		},
		{
			name:    "zero duration",
			input:   "0s",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := timeparse.ParseBefore(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseBefore(%q): expected error, got %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBefore(%q): unexpected error: %v", tc.input, err)
			}
			tc.checkTime(t, got)
		})
	}
}
