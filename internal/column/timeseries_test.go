package column

import (
	"math"
	"testing"
)

func TestTraySeriesKeepsOrderedHistory(t *testing.T) {
	series := NewTraySeries()
	if err := series.Ingest(TimedReading{At: 10, Tray: 1, Pressure: 1.2, Temperature: 100, Present: true}); err != nil {
		t.Fatal(err)
	}
	if err := series.Ingest(TimedReading{At: 10, Tray: 1, Pressure: 1.3, Temperature: 100, Present: true}); err == nil {
		t.Fatal("accepted a reading with the same timestamp")
	}
	if err := series.Ingest(TimedReading{At: 9, Tray: 1, Pressure: 1.3, Temperature: 100, Present: true}); err == nil {
		t.Fatal("accepted an out-of-order reading")
	}
	if err := series.Ingest(TimedReading{At: 20, Tray: 1, Pressure: 1.3, Temperature: 101, Present: true}); err != nil {
		t.Fatal(err)
	}
	if series.LastAt() != 20 {
		t.Fatalf("expected lastAt 20, got %d", series.LastAt())
	}
}

func TestTraySeriesLatestDropsMissingTray(t *testing.T) {
	series := NewTraySeries()
	_ = series.Ingest(TimedReading{At: 1, Tray: 1, Pressure: 1.2, Temperature: 100, Present: true})
	_ = series.Ingest(TimedReading{At: 1, Tray: 2, Pressure: 1.0, Temperature: 90, Present: true})
	_ = series.Ingest(Missing(2, 2))

	profile, at, missing := series.Latest()
	if at != 2 {
		t.Fatalf("expected latest at 2, got %d", at)
	}
	if profile.Count() != 1 {
		t.Fatalf("expected only tray 1 present, got %d", profile.Count())
	}
	if len(missing) != 1 || missing[0] != 2 {
		t.Fatalf("expected tray 2 missing, got %v", missing)
	}
}

func TestTraySeriesAsOf(t *testing.T) {
	series := NewTraySeries()
	_ = series.Ingest(TimedReading{At: 5, Tray: 1, Pressure: 1.0, Temperature: 100, Present: true})
	_ = series.Ingest(TimedReading{At: 10, Tray: 1, Pressure: 1.4, Temperature: 104, Present: true})

	profile, missing := series.AsOf(7)
	if profile.Count() != 1 || len(missing) != 0 {
		t.Fatalf("expected tray 1 present at t=7, got count=%d missing=%v", profile.Count(), missing)
	}
	reading, ok := profile.Get(1)
	if !ok || reading.Pressure != 1.0 {
		t.Fatalf("expected pressure 1.0 at t=7, got %+v ok=%v", reading, ok)
	}

	empty, _ := series.AsOf(4)
	if empty.Count() != 0 {
		t.Fatalf("expected no trays before first sample, got %d", empty.Count())
	}
}

func TestTraySeriesRejectsNonNumeric(t *testing.T) {
	series := NewTraySeries()
	if err := series.Ingest(TimedReading{At: 1, Tray: 1, Pressure: 1.0, Temperature: math.NaN(), Present: true}); err == nil {
		t.Fatal("accepted a NaN temperature")
	}
}
