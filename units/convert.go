package units

import (
	"fmt"
	"math"
)

var bitConversionDict = map[string]float64{
	"bit":      1.0,
	"B":        8.0,
	"byte":     8.0,
	"Kbit":     1000.0,
	"kilobit":  1000.0,
	"Kibit":    1024.0,
	"kibibit":  1024.0,
	"KB":       8000.0,
	"kilobyte": 8000.0,
	"KiB":      8.0 * 1024.0,
	"kibibyte": 8.0 * 1024.0,
	"Mbit":     1000000.0,
	"megabit":  1000000.0,
	"Mibit":    1048576.0,
	"mebibit":  1048576.0,
	"MB":       8000000.0,
	"megabyte": 8000000.0,
	"MiB":      8.0 * 1048576.0,
	"mebibyte": 8.0 * 1048576.0,
	"Gbit":     1000000000.0,
	"gigabit":  1000000000.0,
	"Gibit":    1073741824.0,
	"gibibit":  1073741824.0,
	"GB":       8000000000.0,
	"gigabyte": 8000000000.0,
	"GiB":      8.0 * 1073741824.0,
	"gibibyte": 8.0 * 1073741824.0,
}

// Convert converts a speed value from one unit to another, rounded to 3 decimal places.
func Convert(inp float64, inpType string, outType string) (float64, error) {
	inMult, okIn := bitConversionDict[inpType]
	if !okIn {
		return 0, fmt.Errorf("unknown input unit: %s", inpType)
	}

	outMult, okOut := bitConversionDict[outType]
	if !okOut {
		return 0, fmt.Errorf("unknown output unit: %s", outType)
	}

	res := inp * inMult / outMult
	return math.Round(res*1000) / 1000, nil
}
