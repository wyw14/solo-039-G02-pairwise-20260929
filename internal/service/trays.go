package service

import (
	"errors"
	"github.com/wyw14/solo-039/internal/column"
)

// IngestTrays accepts one time-stamped sweep of tray pressure/temperature
// readings, records it, and diagnoses the column against the learned drop
// baseline. Trays listed in expected but absent from readings are recorded as
// missing; with no expected list, every gap between the lowest and highest
// reported tray is treated as missing. A missing tray makes its adjacent
// segments unknown, never normal.
func (p *Plant) IngestTrays(at int64, readings []column.TrayReading, expected []int) (column.Diagnosis, error) {
	profile, missing, sweepAt, err := p.recordSweep(at, readings, expected)
	if err != nil {
		return column.Diagnosis{}, err
	}
	diagnosis := p.TrayBaseline.Diagnose(sweepAt, profile, missing, p.TrayThresholds)
	if err := p.Profile.SetAll(profile.Ordered()); err != nil {
		return column.Diagnosis{}, err
	}
	p.mu.Lock()
	p.lastTrayDiagnosis = &diagnosis
	p.mu.Unlock()
	return diagnosis, nil
}

// LearnTrays records a sweep known to represent healthy operation and trains
// the per-segment pressure-drop / temperature-gradient baseline from it.
func (p *Plant) LearnTrays(at int64, readings []column.TrayReading, expected []int) (int, error) {
	profile, _, _, err := p.recordSweep(at, readings, expected)
	if err != nil {
		return 0, err
	}
	p.TrayBaseline.Observe(profile)
	return profile.Count(), nil
}

// LastTrayDiagnosis returns the most recent diagnosis and whether one exists.
func (p *Plant) LastTrayDiagnosis() (column.Diagnosis, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lastTrayDiagnosis == nil {
		return column.Diagnosis{}, false
	}
	return *p.lastTrayDiagnosis, true
}

func (p *Plant) recordSweep(at int64, readings []column.TrayReading, expected []int) (*column.Profile, []int, int64, error) {
	if at <= 0 {
		return nil, nil, 0, errors.New("sweep timestamp is required")
	}
	if len(readings) == 0 {
		return nil, nil, 0, errors.New("sweep contains no tray readings")
	}

	// Build this sweep's profile from the batch alone. The series is an append
	// history; diagnosis must reflect what this sweep measured, not a stale
	// per-tray latest marker from an earlier sweep.
	profile := column.NewProfile()
	if err := profile.SetAll(readings); err != nil {
		return nil, nil, 0, err
	}

	present := make(map[int]struct{}, len(readings))
	minTray, maxTray := readings[0].Tray, readings[0].Tray
	for _, reading := range readings {
		present[reading.Tray] = struct{}{}
		if reading.Tray < minTray {
			minTray = reading.Tray
		}
		if reading.Tray > maxTray {
			maxTray = reading.Tray
		}
		if err := p.TraySeries.Ingest(column.TimedReading{At: at, Tray: reading.Tray, Temperature: reading.Temperature, Pressure: reading.Pressure, Present: true}); err != nil {
			return nil, nil, 0, err
		}
	}

	expectedSet, err := resolveExpected(expected, minTray, maxTray)
	if err != nil {
		return nil, nil, 0, err
	}
	missing := make([]int, 0)
	for tray := range expectedSet {
		if _, ok := present[tray]; ok {
			continue
		}
		missing = append(missing, tray)
		// A missing marker older than that tray's newest reading is rejected by
		// the series; that is fine, history keeps the truth.
		_ = p.TraySeries.Ingest(column.Missing(at, tray))
	}

	return profile, missing, at, nil
}

func resolveExpected(expected []int, minTray, maxTray int) (map[int]struct{}, error) {
	set := make(map[int]struct{})
	if len(expected) == 0 {
		for tray := minTray; tray <= maxTray; tray++ {
			set[tray] = struct{}{}
		}
		return set, nil
	}
	for _, tray := range expected {
		if tray < column.MinTray || tray > column.MaxTray {
			return nil, errors.New("expected tray outside configured column")
		}
		if _, dup := set[tray]; dup {
			return nil, errors.New("duplicate expected tray")
		}
		set[tray] = struct{}{}
	}
	return set, nil
}
