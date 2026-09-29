package column

import (
	"math"
	"testing"
)

// healthyProfile builds five trays whose every segment has a 0.20 pressure
// drop and a -8/tray temperature gradient.
func healthyProfile() *Profile {
	p := NewProfile()
	readings := []TrayReading{
		{Tray: 1, Pressure: 1.80, Temperature: 120},
		{Tray: 2, Pressure: 1.60, Temperature: 112},
		{Tray: 3, Pressure: 1.40, Temperature: 104},
		{Tray: 4, Pressure: 1.20, Temperature: 96},
		{Tray: 5, Pressure: 1.00, Temperature: 88},
	}
	for _, reading := range readings {
		if err := p.Put(reading); err != nil {
			panic(err)
		}
	}
	return p
}

func trainedBaseline() *DropBaseline {
	b := NewDropBaseline()
	for range 3 {
		b.Observe(healthyProfile())
	}
	return b
}

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDiagnoseHealthyProfile(t *testing.T) {
	b := trainedBaseline()
	diagnosis := b.Diagnose(1, healthyProfile(), nil, DefaultThresholds())
	if !diagnosis.Healthy {
		t.Fatalf("expected healthy, got anomalies=%v unknown=%v", diagnosis.Anomalies, diagnosis.Unknown)
	}
	if len(diagnosis.Anomalies) != 0 || len(diagnosis.Unknown) != 0 {
		t.Fatalf("expected no flagged segments, got %+v", diagnosis)
	}
}

func TestDiagnoseFlagsFloodingWithEvidence(t *testing.T) {
	b := trainedBaseline()
	live := healthyProfile()
	// Raising tray 3 pressure spikes the segment below it (trays 3 -> 4) to
	// 1.6x baseline while easing the segment above it.
	if err := live.Put(TrayReading{Tray: 3, Pressure: 1.52, Temperature: 104}); err != nil {
		t.Fatal(err)
	}
	diagnosis := b.Diagnose(2, live, nil, DefaultThresholds())
	if diagnosis.Healthy {
		t.Fatal("flooding profile reported healthy")
	}
	if len(diagnosis.Anomalies) != 1 {
		t.Fatalf("expected one anomaly, got %d", len(diagnosis.Anomalies))
	}
	anomaly := diagnosis.Anomalies[0]
	if anomaly.Status != SegmentFlooding || anomaly.Lower != 3 || anomaly.Upper != 4 {
		t.Fatalf("expected flooding on trays 3-4, got %+v", anomaly)
	}
	if !almost(anomaly.Evidence.Drop, 0.32) || !almost(anomaly.Evidence.BaselineDrop, 0.20) {
		t.Fatalf("unexpected drop evidence %+v", anomaly.Evidence)
	}
	if math.Abs(anomaly.Evidence.DropRatio-1.6) > 1e-9 {
		t.Fatalf("expected ratio 1.6, got %v", anomaly.Evidence.DropRatio)
	}
	if len(anomaly.Reasons) == 0 {
		t.Fatal("anomaly carries no human-readable evidence")
	}
}

func TestDiagnoseFlagsFoulingBand(t *testing.T) {
	b := trainedBaseline()
	live := healthyProfile()
	// 0.26 drop = 1.3x baseline sits in the mild fouling band [1.2, 1.5).
	if err := live.Put(TrayReading{Tray: 3, Pressure: 1.46, Temperature: 104}); err != nil {
		t.Fatal(err)
	}
	diagnosis := b.Diagnose(3, live, nil, DefaultThresholds())
	if len(diagnosis.Anomalies) != 1 || diagnosis.Anomalies[0].Status != SegmentFouling {
		t.Fatalf("expected one fouling segment, got %+v", diagnosis.Anomalies)
	}
}

func TestDiagnoseMissingTrayIsUnknownNotNormal(t *testing.T) {
	b := trainedBaseline()
	live := NewProfile()
	for _, reading := range []TrayReading{
		{Tray: 1, Pressure: 1.80, Temperature: 120},
		{Tray: 2, Pressure: 1.60, Temperature: 112},
		// tray 3 instrument silent
		{Tray: 4, Pressure: 1.20, Temperature: 96},
		{Tray: 5, Pressure: 1.00, Temperature: 88},
	} {
		if err := live.Put(reading); err != nil {
			t.Fatal(err)
		}
	}
	diagnosis := b.Diagnose(4, live, []int{3}, DefaultThresholds())
	if diagnosis.Healthy {
		t.Fatal("column with a missing tray reported healthy")
	}
	if len(diagnosis.Anomalies) != 0 {
		t.Fatalf("missing data must not be diagnosed as an anomaly: %+v", diagnosis.Anomalies)
	}
	if len(diagnosis.Unknown) != 1 {
		t.Fatalf("expected one merged unknown stretch, got %d", len(diagnosis.Unknown))
	}
	unknown := diagnosis.Unknown[0]
	if unknown.Lower != 2 || unknown.Upper != 4 {
		t.Fatalf("expected unknown stretch trays 2-4, got %d-%d", unknown.Lower, unknown.Upper)
	}
	if len(diagnosis.MissingTrays) != 1 || diagnosis.MissingTrays[0] != 3 {
		t.Fatalf("expected tray 3 listed missing, got %v", diagnosis.MissingTrays)
	}
}

func TestDiagnoseSegmentWithoutBaselineIsUnknown(t *testing.T) {
	// Baseline only learned the trays 1-2 and 2-3 segments.
	b := NewDropBaseline()
	partial := NewProfile()
	for _, reading := range []TrayReading{
		{Tray: 1, Pressure: 1.80, Temperature: 120},
		{Tray: 2, Pressure: 1.60, Temperature: 112},
		{Tray: 3, Pressure: 1.40, Temperature: 104},
	} {
		_ = partial.Put(reading)
	}
	for range 3 {
		b.Observe(partial)
	}
	diagnosis := b.Diagnose(5, healthyProfile(), nil, DefaultThresholds())
	if diagnosis.Healthy {
		t.Fatal("unlearned segments must prevent a healthy verdict")
	}
	found := false
	for _, segment := range diagnosis.Unknown {
		if segment.Lower == 3 && segment.Upper == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unlearned stretch trays 3-5 unknown, got %+v", diagnosis.Unknown)
	}
}

func TestDiagnoseMergesConsecutiveFloodingSegments(t *testing.T) {
	b := trainedBaseline()
	live := healthyProfile()
	// Pressurising two consecutive trays overloads both segments beneath the
	// flooded band (3-4 at 1.6x and 4-5 at 1.75x baseline).
	_ = live.Put(TrayReading{Tray: 3, Pressure: 1.67, Temperature: 104})
	_ = live.Put(TrayReading{Tray: 4, Pressure: 1.35, Temperature: 96})
	diagnosis := b.Diagnose(6, live, nil, DefaultThresholds())
	if len(diagnosis.Anomalies) != 1 {
		t.Fatalf("expected a single merged anomaly, got %+v", diagnosis.Anomalies)
	}
	anomaly := diagnosis.Anomalies[0]
	if anomaly.Status != SegmentFlooding {
		t.Fatalf("expected flooding, got %q", anomaly.Status)
	}
	if anomaly.Lower != 3 || anomaly.Upper != 5 {
		t.Fatalf("expected flooding stretch 3-5, got %d-%d", anomaly.Lower, anomaly.Upper)
	}
}

func TestProfileSetAllRejectsBatchAtomically(t *testing.T) {
	p := healthyProfile()
	before := p.Count()
	err := p.SetAll([]TrayReading{
		{Tray: 1, Pressure: 1, Temperature: 1},
		{Tray: 999, Pressure: 1, Temperature: 1},
	})
	if err == nil {
		t.Fatal("expected out-of-range rejection")
	}
	if p.Count() != before {
		t.Fatalf("profile changed after failed batch: %d vs %d", p.Count(), before)
	}
}

func TestProfileGaps(t *testing.T) {
	p := NewProfile()
	_ = p.Put(TrayReading{Tray: 1, Pressure: 1, Temperature: 1})
	_ = p.Put(TrayReading{Tray: 4, Pressure: 1, Temperature: 1})
	gaps := p.Gaps()
	if len(gaps) != 2 || gaps[0] != 2 || gaps[1] != 3 {
		t.Fatalf("expected gaps [2 3], got %v", gaps)
	}
}
