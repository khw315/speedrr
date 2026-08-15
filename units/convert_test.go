package units

import (
	"math"
	"testing"
)

func TestConvert(t *testing.T) {
	tests := []struct {
		inp      float64
		inpType  string
		outType  string
		expected float64
	}{
		{10, "Mbit", "B", 1250000},
		{10, "Mbit", "KB", 1250},
		{1000, "Kbit", "Mbit", 1},
		{1, "GB", "MB", 1000},
		{1, "GiB", "MiB", 1024},
		{8, "bit", "B", 1},
	}

	for _, tt := range tests {
		got, err := Convert(tt.inp, tt.inpType, tt.outType)
		if err != nil {
			t.Errorf("Convert(%v, %s, %s) unexpected error: %v", tt.inp, tt.inpType, tt.outType, err)
		}
		if math.Abs(got-tt.expected) > 0.001 {
			t.Errorf("Convert(%v, %s, %s) = %v; want %v", tt.inp, tt.inpType, tt.outType, got, tt.expected)
		}
	}

	// Error test cases
	if _, err := Convert(10, "invalid_unit", "B"); err == nil {
		t.Errorf("Expected error for invalid input unit, got nil")
	}
	if _, err := Convert(10, "Mbit", "invalid_unit"); err == nil {
		t.Errorf("Expected error for invalid output unit, got nil")
	}
}
