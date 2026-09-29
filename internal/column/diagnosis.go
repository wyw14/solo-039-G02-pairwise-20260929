package column

import (
	"fmt"
	"sort"
	"sync"
)

// SegmentStatus classifies the stretch of column between two instrumented
// trays. unknown is deliberately distinct from normal: a segment that cannot
// be judged (missing instrument, or no learned history) must never read as
// healthy.
type SegmentStatus string

const (
	SegmentNormal   SegmentStatus = "normal"
	SegmentUnknown  SegmentStatus = "unknown"
	SegmentFlooding SegmentStatus = "flooding"
	SegmentFouling  SegmentStatus = "fouling"
)

// Thresholds tunes the diagnosis. Zero values fall back to DefaultThresholds.
type Thresholds struct {
	// MinSamples is the number of historical observations a segment needs
	// before it can be judged against a baseline.
	MinSamples int
	// FloodDropRatio flags flooding when the live pressure drop reaches this
	// many times the historical mean: a sharp hydraulic overload that backs
	// liquid up the column.
	FloodDropRatio float64
	// FloorDrop is the smallest absolute excess drop (pressure units) that
	// counts as an anomaly, guarding against large ratios off a near-zero
	// baseline.
	FloorDrop float64
	// FoulingDropRatio flags the milder, sustained rise caused by fouling /
	// blocked downcomers. A segment at or above this but below FloodDropRatio
	// is reported as fouling.
	FoulingDropRatio float64
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		MinSamples:       3,
		FloodDropRatio:   1.5,
		FloorDrop:        0.0,
		FoulingDropRatio: 1.2,
	}
}

func (t Thresholds) withDefaults() Thresholds {
	d := DefaultThresholds()
	if t.MinSamples <= 0 {
		t.MinSamples = d.MinSamples
	}
	if t.FloodDropRatio <= 0 {
		t.FloodDropRatio = d.FloodDropRatio
	}
	if t.FoulingDropRatio <= 0 {
		t.FoulingDropRatio = d.FoulingDropRatio
	}
	return t
}

// SegmentEvidence carries the measured numbers behind a verdict so that an
// operator can see why a segment was flagged.
type SegmentEvidence struct {
	Drop             float64 `json:"drop"`
	BaselineDrop     float64 `json:"baseline_drop"`
	DropRatio        float64 `json:"drop_ratio"`
	Samples          int     `json:"samples"`
	Gradient         float64 `json:"gradient"`
	BaselineGradient float64 `json:"baseline_gradient"`
	GradientRatio    float64 `json:"gradient_ratio"`
}

// Segment is a diagnosed stretch from Lower (below) to Upper (above).
type Segment struct {
	Lower    int             `json:"lower_tray"`
	Upper    int             `json:"upper_tray"`
	Status   SegmentStatus   `json:"status"`
	Evidence SegmentEvidence `json:"evidence"`
	Reasons  []string        `json:"reasons"`
}

// Diagnosis is the verdict for one point in time. Anomalies lists flooding /
// fouling stretches; Unknown lists stretches that could not be judged. Healthy
// is true only when every segment is normal.
type Diagnosis struct {
	At           int64     `json:"at"`
	TrayCount    int       `json:"tray_count"`
	MissingTrays []int     `json:"missing_trays"`
	Anomalies    []Segment `json:"anomalies"`
	Unknown      []Segment `json:"unknown"`
	Healthy      bool      `json:"healthy"`
}

type pairKey struct{ lower, upper int }

type baselineStat struct {
	sumDrop float64
	nDrop   int
	sumGrad float64
	nGrad   int
}

// DropBaseline learns the healthy pressure drop and temperature gradient of
// each adjacent instrumented segment from historical profiles.
type DropBaseline struct {
	mu    sync.Mutex
	stats map[pairKey]*baselineStat
}

func NewDropBaseline() *DropBaseline {
	return &DropBaseline{stats: make(map[pairKey]*baselineStat)}
}

// Observe trains the baseline from one profile considered healthy. Each pair
// of adjacent present trays contributes one observation of that segment.
func (b *DropBaseline) Observe(p *Profile) {
	values := p.Ordered()
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 1; i < len(values); i++ {
		lower, upper := values[i-1], values[i]
		key := pairKey{lower.Tray, upper.Tray}
		stat := b.stats[key]
		if stat == nil {
			stat = &baselineStat{}
			b.stats[key] = stat
		}
		span := float64(upper.Tray - lower.Tray)
		stat.sumDrop += lower.Pressure - upper.Pressure
		stat.nDrop++
		if span > 0 {
			stat.sumGrad += (upper.Temperature - lower.Temperature) / span
			stat.nGrad++
		}
	}
}

// Samples reports how many historical observations exist for one segment.
func (b *DropBaseline) Samples(lower, upper int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if stat := b.stats[pairKey{lower, upper}]; stat != nil {
		return stat.nDrop
	}
	return 0
}

type edge struct {
	lower       int
	upper       int
	lowerStatus trayState
	upperStatus trayState
	drop        float64
	gradient    float64
}

type trayState struct {
	tray    int
	present bool
}

// Diagnose compares each adjacent segment of the live profile to its learned
// baseline. missing lists trays expected but not reported this sweep; any edge
// touching a missing tray, and any segment without enough baseline history, is
// returned as unknown rather than normal.
func (b *DropBaseline) Diagnose(at int64, p *Profile, missing []int, thresholds Thresholds) Diagnosis {
	thresholds = thresholds.withDefaults()
	missingSet := make(map[int]struct{}, len(missing))
	for _, tray := range missing {
		missingSet[tray] = struct{}{}
	}

	states := make([]trayState, 0, p.Count()+len(missingSet))
	for _, reading := range p.Ordered() {
		states = append(states, trayState{tray: reading.Tray, present: true})
	}
	for tray := range missingSet {
		states = append(states, trayState{tray: tray, present: false})
	}
	sort.Slice(states, func(i, j int) bool { return states[i].tray < states[j].tray })

	edges := make([]edge, 0, len(states))
	readings := p.Ordered()
	readingAt := make(map[int]TrayReading, len(readings))
	for _, reading := range readings {
		readingAt[reading.Tray] = reading
	}
	for i := 1; i < len(states); i++ {
		low, high := states[i-1], states[i]
		e := edge{lower: low.tray, upper: high.tray, lowerStatus: low, upperStatus: high}
		if low.present && high.present {
			lo, up := readingAt[low.tray], readingAt[high.tray]
			e.drop = lo.Pressure - up.Pressure
			if span := float64(high.tray - low.tray); span > 0 {
				e.gradient = (up.Temperature - lo.Temperature) / span
			}
		}
		edges = append(edges, e)
	}

	b.mu.Lock()
	snapshot := make(map[pairKey]baselineStat, len(b.stats))
	for key, stat := range b.stats {
		snapshot[key] = *stat
	}
	b.mu.Unlock()

	segments := make([]Segment, 0, len(edges))
	for _, e := range edges {
		segments = append(segments, classifyEdge(e, snapshot, thresholds))
	}

	anomalies, unknown := mergeSegments(segments)
	missingSorted := append([]int(nil), missing...)
	sort.Ints(missingSorted)

	return Diagnosis{
		At:           at,
		TrayCount:    p.Count(),
		MissingTrays: missingSorted,
		Anomalies:    anomalies,
		Unknown:      unknown,
		Healthy:      len(anomalies) == 0 && len(unknown) == 0 && len(edges) > 0,
	}
}

func classifyEdge(e edge, baseline map[pairKey]baselineStat, thr Thresholds) Segment {
	seg := Segment{Lower: e.lower, Upper: e.upper}

	if !e.lowerStatus.present || !e.upperStatus.present {
		seg.Status = SegmentUnknown
		absent := make([]int, 0, 2)
		if !e.lowerStatus.present {
			absent = append(absent, e.lower)
		}
		if !e.upperStatus.present {
			absent = append(absent, e.upper)
		}
		for _, tray := range absent {
			seg.Reasons = append(seg.Reasons, fmt.Sprintf("tray %d reading missing", tray))
		}
		return seg
	}

	stat, learned := baseline[pairKey{e.lower, e.upper}]
	evidence := SegmentEvidence{Drop: e.drop, Gradient: e.gradient}
	if learned {
		evidence.BaselineDrop = stat.sumDrop / float64(stat.nDrop)
		evidence.Samples = stat.nDrop
		if stat.nDrop > 0 && evidence.BaselineDrop != 0 {
			evidence.DropRatio = e.drop / evidence.BaselineDrop
		}
		if stat.nGrad > 0 {
			evidence.BaselineGradient = stat.sumGrad / float64(stat.nGrad)
			if evidence.BaselineGradient != 0 {
				evidence.GradientRatio = e.gradient / evidence.BaselineGradient
			}
		}
	}
	seg.Evidence = evidence

	if !learned || stat.nDrop < thr.MinSamples {
		seg.Status = SegmentUnknown
		seg.Reasons = []string{fmt.Sprintf("insufficient baseline history: %d/%d samples", evidence.Samples, thr.MinSamples)}
		return seg
	}

	excess := e.drop - evidence.BaselineDrop
	if excess >= thr.FloorDrop && e.drop >= evidence.BaselineDrop*thr.FoulingDropRatio {
		// A severe hydraulic spike is flooding; a milder sustained rise is the
		// signature of fouling or a blocked downcomer.
		if e.drop >= evidence.BaselineDrop*thr.FloodDropRatio {
			seg.Status = SegmentFlooding
			seg.Reasons = []string{
				fmt.Sprintf("pressure drop %.4f is %.2fx baseline %.4f across %d samples", e.drop, evidence.DropRatio, evidence.BaselineDrop, stat.nDrop),
			}
		} else {
			seg.Status = SegmentFouling
			seg.Reasons = []string{
				fmt.Sprintf("pressure drop %.4f is %.2fx baseline %.4f, a sustained rise across %d samples", e.drop, evidence.DropRatio, evidence.BaselineDrop, stat.nDrop),
			}
			if evidence.BaselineGradient != 0 {
				seg.Reasons = append(seg.Reasons, fmt.Sprintf("temperature gradient %.4f vs baseline %.4f", e.gradient, evidence.BaselineGradient))
			}
		}
		return seg
	}

	seg.Status = SegmentNormal
	return seg
}

// mergeSegments joins consecutive edges that share the same abnormal status
// into one stretch, keeping the strongest (highest drop ratio) evidence.
func mergeSegments(segments []Segment) (anomalies, unknown []Segment) {
	flush := func(run []Segment, bucket *[]Segment) {
		if len(run) == 0 {
			return
		}
		merged := run[0]
		merged.Upper = run[len(run)-1].Upper
		peak := 0
		reasons := []string{}
		seen := map[string]struct{}{}
		for i, seg := range run {
			if seg.Evidence.DropRatio > run[peak].Evidence.DropRatio {
				peak = i
			}
			for _, reason := range seg.Reasons {
				if _, ok := seen[reason]; ok {
					continue
				}
				seen[reason] = struct{}{}
				reasons = append(reasons, reason)
			}
		}
		merged.Evidence = run[peak].Evidence
		merged.Reasons = reasons
		if len(run) > 1 {
			reasons = append(reasons, fmt.Sprintf("spans %d adjacent segments", len(run)))
			merged.Reasons = reasons
		}
		*bucket = append(*bucket, merged)
	}

	run := []Segment{}
	var runStatus SegmentStatus
	for _, seg := range segments {
		abnormal := seg.Status == SegmentFlooding || seg.Status == SegmentFouling || seg.Status == SegmentUnknown
		if abnormal && seg.Status == runStatus && len(run) > 0 {
			run = append(run, seg)
			continue
		}
		if runStatus == SegmentFlooding || runStatus == SegmentFouling {
			flush(run, &anomalies)
		} else if runStatus == SegmentUnknown {
			flush(run, &unknown)
		}
		if abnormal {
			run = []Segment{seg}
			runStatus = seg.Status
		} else {
			run = nil
			runStatus = SegmentNormal
		}
	}
	if runStatus == SegmentFlooding || runStatus == SegmentFouling {
		flush(run, &anomalies)
	} else if runStatus == SegmentUnknown {
		flush(run, &unknown)
	}
	return anomalies, unknown
}
