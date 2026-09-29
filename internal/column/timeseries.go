package column

import (
	"errors"
	"sort"
	"sync"
)

// TimedReading is a pressure/temperature measurement taken from one tray at a
// point in time. A reading with Present == false records that a tray was
// expected but its instrument did not deliver a value: the tray is missing,
// not healthy.
type TimedReading struct {
	At          int64   `json:"at"`
	Tray        int     `json:"tray"`
	Temperature float64 `json:"temperature"`
	Pressure    float64 `json:"pressure"`
	Present     bool    `json:"present"`
}

// Missing builds a marker for a tray that was not reported in a sweep.
func Missing(at int64, tray int) TimedReading {
	return TimedReading{At: at, Tray: tray, Present: false}
}

// TraySeries keeps the history of each tray's readings in arrival order. A
// reading is accepted only if it is newer than the tray's previous one, so a
// late or replayed sweep cannot move a tray backwards in time.
type TraySeries struct {
	mu     sync.Mutex
	series map[int][]TimedReading
	lastAt int64
}

func NewTraySeries() *TraySeries {
	return &TraySeries{series: make(map[int][]TimedReading)}
}

// Ingest stores one timestamped reading. Out-of-range trays and non-finite
// values are rejected; a Present == false marker is always accepted.
func (s *TraySeries) Ingest(reading TimedReading) error {
	if reading.Tray < MinTray || reading.Tray > MaxTray {
		return errors.New("tray outside configured column")
	}
	if reading.Present {
		if err := validateReading(TrayReading{Tray: reading.Tray, Temperature: reading.Temperature, Pressure: reading.Pressure}); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prior := s.series[reading.Tray]
	if len(prior) > 0 && reading.At <= prior[len(prior)-1].At {
		return errors.New("tray reading is not newer than previous sample")
	}
	s.series[reading.Tray] = append(prior, reading)
	if reading.At > s.lastAt {
		s.lastAt = reading.At
	}
	return nil
}

// Latest returns the most recent reading known for each tray. A tray whose
// latest marker is Present == false is omitted from the profile and returned in
// missing, so a dead instrument is never mistaken for a healthy tray.
func (s *TraySeries) Latest() (profile *Profile, at int64, missing []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile = NewProfile()
	trays := make([]int, 0, len(s.series))
	for tray := range s.series {
		trays = append(trays, tray)
	}
	sort.Ints(trays)
	latest := s.lastAt
	for _, tray := range trays {
		last := s.series[tray][len(s.series[tray])-1]
		if last.Present {
			profile.readings[tray] = TrayReading{Tray: tray, Temperature: last.Temperature, Pressure: last.Pressure}
		} else {
			missing = append(missing, tray)
		}
	}
	return profile, latest, missing
}

// AsOf returns the newest reading per tray that is not newer than at. Trays
// with no reading by that time are reported as missing.
func (s *TraySeries) AsOf(at int64) (profile *Profile, missing []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile = NewProfile()
	trays := make([]int, 0, len(s.series))
	for tray := range s.series {
		trays = append(trays, tray)
	}
	sort.Ints(trays)
	for _, tray := range trays {
		found := TimedReading{}
		for _, reading := range s.series[tray] {
			if reading.At <= at {
				found = reading
			} else {
				break
			}
		}
		if found.Present {
			profile.readings[tray] = TrayReading{Tray: tray, Temperature: found.Temperature, Pressure: found.Pressure}
		} else {
			missing = append(missing, tray)
		}
	}
	return profile, missing
}

func (s *TraySeries) LastAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAt
}

func (s *TraySeries) TrayCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.series)
}
