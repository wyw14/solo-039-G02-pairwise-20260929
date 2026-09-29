package column

import (
	"errors"
	"math"
	"sort"
)

const (
	MinTray = 1
	MaxTray = 200
)

type TrayReading struct {
	Tray        int     `json:"tray"`
	Temperature float64 `json:"temperature"`
	Pressure    float64 `json:"pressure"`
}

type Profile struct{ readings map[int]TrayReading }

func NewProfile() *Profile { return &Profile{readings: make(map[int]TrayReading)} }

func validateReading(reading TrayReading) error {
	if reading.Tray < MinTray || reading.Tray > MaxTray {
		return errors.New("tray outside configured column")
	}
	if math.IsNaN(reading.Temperature) || math.IsNaN(reading.Pressure) ||
		math.IsInf(reading.Temperature, 0) || math.IsInf(reading.Pressure, 0) {
		return errors.New("tray reading is not numeric")
	}
	return nil
}

func (p *Profile) Put(reading TrayReading) error {
	if err := validateReading(reading); err != nil {
		return err
	}
	p.readings[reading.Tray] = reading
	return nil
}

// SetAll replaces the whole profile with the given readings. Nothing is
// replaced unless every reading is valid and no tray is reported twice, so a
// batch containing a bad value never leaves a partially applied profile.
func (p *Profile) SetAll(readings []TrayReading) error {
	next := make(map[int]TrayReading, len(readings))
	for _, reading := range readings {
		if err := validateReading(reading); err != nil {
			return err
		}
		if _, exists := next[reading.Tray]; exists {
			return errors.New("duplicate tray in batch")
		}
		next[reading.Tray] = reading
	}
	p.readings = next
	return nil
}

func (p *Profile) Ordered() []TrayReading {
	keys := make([]int, 0, len(p.readings))
	for tray := range p.readings {
		keys = append(keys, tray)
	}
	sort.Ints(keys)
	out := make([]TrayReading, 0, len(keys))
	for _, tray := range keys {
		out = append(out, p.readings[tray])
	}
	return out
}

// Get returns the reading for a tray and whether it is present.
func (p *Profile) Get(tray int) (TrayReading, bool) {
	reading, ok := p.readings[tray]
	return reading, ok
}

// Gaps returns the tray numbers missing inside the covered span (between the
// lowest and highest instrumented tray). Trays beyond that span are not gaps.
func (p *Profile) Gaps() []int {
	values := p.Ordered()
	if len(values) < 2 {
		return nil
	}
	gaps := make([]int, 0)
	for index := 1; index < len(values); index++ {
		for tray := values[index-1].Tray + 1; tray < values[index].Tray; tray++ {
			gaps = append(gaps, tray)
		}
	}
	return gaps
}

// PressureDrop returns the pressure loss between two instrumented trays,
// measured in the upflow direction (lower tray minus upper tray).
func (p *Profile) PressureDrop(lower, upper int) (float64, bool) {
	lo, okLow := p.readings[lower]
	up, okUp := p.readings[upper]
	if !okLow || !okUp {
		return 0, false
	}
	return lo.Pressure - up.Pressure, true
}

func (p *Profile) Gradient() (float64, error) {
	values := p.Ordered()
	if len(values) < 2 {
		return 0, errors.New("profile needs at least two trays")
	}
	first := values[0]
	last := values[len(values)-1]
	return (last.Temperature - first.Temperature) / float64(last.Tray-first.Tray), nil
}

func (p *Profile) Stable(maxDelta float64) bool {
	values := p.Ordered()
	if len(values) < 2 {
		return false
	}
	for index := 1; index < len(values); index++ {
		delta := values[index].Temperature - values[index-1].Temperature
		if delta > maxDelta || delta < -maxDelta {
			return false
		}
	}
	return true
}

func (p *Profile) Count() int { return len(p.readings) }
