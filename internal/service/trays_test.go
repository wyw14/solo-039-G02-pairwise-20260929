package service

import (
	"testing"

	"github.com/wyw14/solo-039/internal/column"
)

func traySweep(p map[int]float64) []column.TrayReading {
	readings := make([]column.TrayReading, 0, len(p))
	for tray, pressure := range p {
		readings = append(readings, column.TrayReading{Tray: tray, Pressure: pressure, Temperature: 120 - float64(tray-1)*8})
	}
	return readings
}

func healthyPressures() map[int]float64 {
	return map[int]float64{1: 1.80, 2: 1.60, 3: 1.40, 4: 1.20, 5: 1.00}
}

func newTrayTestPlant(t *testing.T) *Plant {
	t.Helper()
	return NewPlant(t.TempDir())
}

func TestIngestTraysLearnsBaselineThenDiagnosesHealthy(t *testing.T) {
	plant := newTrayTestPlant(t)
	for at := int64(1); at <= 3; at++ {
		if _, err := plant.LearnTrays(at, traySweep(healthyPressures()), nil); err != nil {
			t.Fatalf("learn sweep %d: %v", at, err)
		}
	}
	diagnosis, err := plant.IngestTrays(10, traySweep(healthyPressures()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosis.Healthy {
		t.Fatalf("expected healthy diagnosis, got anomalies=%v unknown=%v", diagnosis.Anomalies, diagnosis.Unknown)
	}
	if got, ok := plant.LastTrayDiagnosis(); !ok || got.At != 10 {
		t.Fatalf("last diagnosis not stored: %+v ok=%v", got, ok)
	}
	if plant.Status()["tray_diagnosis"] == nil {
		t.Fatal("status missing tray_diagnosis")
	}
}

func TestIngestTraysLocatesFloodingStartAndEnd(t *testing.T) {
	plant := newTrayTestPlant(t)
	for at := int64(1); at <= 3; at++ {
		_, _ = plant.LearnTrays(at, traySweep(healthyPressures()), nil)
	}
	live := healthyPressures()
	live[3] = 1.52 // spikes the 3-4 segment to 1.6x
	diagnosis, err := plant.IngestTrays(20, traySweep(live), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnosis.Anomalies) != 1 {
		t.Fatalf("expected one anomaly, got %+v", diagnosis.Anomalies)
	}
	anomaly := diagnosis.Anomalies[0]
	if anomaly.Status != column.SegmentFlooding || anomaly.Lower != 3 || anomaly.Upper != 4 {
		t.Fatalf("expected flooding from tray 3 to 4, got %+v", anomaly)
	}
	if len(anomaly.Reasons) == 0 {
		t.Fatal("anomaly must carry evidence")
	}
}

func TestIngestTraysMissingInstrumentMarksUnknown(t *testing.T) {
	plant := newTrayTestPlant(t)
	for at := int64(1); at <= 3; at++ {
		_, _ = plant.LearnTrays(at, traySweep(healthyPressures()), []int{1, 2, 3, 4, 5})
	}
	pressures := healthyPressures()
	delete(pressures, 3) // tray 3 instrument silent
	diagnosis, err := plant.IngestTrays(30, traySweep(pressures), []int{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.Healthy {
		t.Fatal("a missing instrument must never read healthy")
	}
	if len(diagnosis.Anomalies) != 0 {
		t.Fatalf("missing data is not an anomaly, got %+v", diagnosis.Anomalies)
	}
	if len(diagnosis.Unknown) != 1 || diagnosis.Unknown[0].Lower != 2 || diagnosis.Unknown[0].Upper != 4 {
		t.Fatalf("expected unknown stretch 2-4, got %+v", diagnosis.Unknown)
	}
	if len(diagnosis.MissingTrays) != 1 || diagnosis.MissingTrays[0] != 3 {
		t.Fatalf("expected tray 3 reported missing, got %v", diagnosis.MissingTrays)
	}
}

func TestIngestTraysRejectsStaleAndEmpty(t *testing.T) {
	plant := newTrayTestPlant(t)
	if _, err := plant.IngestTrays(0, traySweep(healthyPressures()), nil); err == nil {
		t.Fatal("expected error for missing timestamp")
	}
	if _, err := plant.IngestTrays(1, nil, nil); err == nil {
		t.Fatal("expected error for empty sweep")
	}
	_, err := plant.IngestTrays(1, traySweep(healthyPressures()), nil)
	if err != nil {
		t.Fatal(err)
	}
	// No baseline yet: every segment must come back unknown, not normal.
	diagnosis, _ := plant.IngestTrays(2, traySweep(healthyPressures()), nil)
	if diagnosis.Healthy || len(diagnosis.Unknown) == 0 {
		t.Fatalf("unlearned column should be fully unknown, got %+v", diagnosis)
	}
}
