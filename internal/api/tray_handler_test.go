package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wyw14/solo-039/internal/column"
	"github.com/wyw14/solo-039/internal/service"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server := NewServer()
	server.plant = service.NewPlant(t.TempDir())
	return server
}

func postSweep(t *testing.T, s *Server, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func baselineReadings() []column.TrayReading {
	return []column.TrayReading{
		{Tray: 1, Pressure: 1.8, Temperature: 120},
		{Tray: 2, Pressure: 1.6, Temperature: 112},
		{Tray: 3, Pressure: 1.4, Temperature: 104},
	}
}

func TestTrayDiagnosisEndToEnd(t *testing.T) {
	s := newTestServer(t)

	// No sweep yet -> 404.
	req := httptest.NewRequest(http.MethodGet, "/api/profile/diagnosis", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before any sweep, got %d", rec.Code)
	}

	// Teach three healthy sweeps.
	for at := int64(1); at <= 3; at++ {
		if code, body := postSweep(t, s, "/api/profile/baseline", map[string]any{"at": at, "readings": baselineReadings()}); code != http.StatusAccepted {
			t.Fatalf("learn sweep %d -> %d %v", at, code, body)
		}
	}

	// A flooding sweep: raise tray 2 so the 2-3 segment hits 1.5x baseline.
	flooding := baselineReadings()
	flooding[1].Pressure = 1.70
	code, body := postSweep(t, s, "/api/profile/readings", map[string]any{"at": 10, "readings": flooding})
	if code != http.StatusAccepted {
		t.Fatalf("ingest -> %d %v", code, body)
	}
	anomalies, _ := body["anomalies"].([]any)
	if len(anomalies) != 1 {
		t.Fatalf("expected one anomaly in response, got %v", body)
	}

	// Diagnosis endpoint now returns 200.
	req = httptest.NewRequest(http.MethodGet, "/api/profile/diagnosis", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after sweep, got %d", rec.Code)
	}
}

func TestTrayIngestMissingTrayIsUnknown(t *testing.T) {
	s := newTestServer(t)
	for at := int64(1); at <= 3; at++ {
		_, _ = postSweep(t, s, "/api/profile/baseline", map[string]any{"at": at, "readings": baselineReadings(), "expected": []int{1, 2, 3}})
	}
	code, body := postSweep(t, s, "/api/profile/readings", map[string]any{
		"at":       9,
		"readings": []column.TrayReading{{Tray: 1, Pressure: 1.8, Temperature: 120}, {Tray: 3, Pressure: 1.4, Temperature: 104}},
		"expected": []int{1, 2, 3},
	})
	if code != http.StatusAccepted {
		t.Fatalf("ingest -> %d %v", code, body)
	}
	if healthy, _ := body["healthy"].(bool); healthy {
		t.Fatal("missing tray must not report healthy")
	}
	unknown, _ := body["unknown"].([]any)
	if len(unknown) != 1 {
		t.Fatalf("expected one unknown stretch, got %v", body["unknown"])
	}
}

func TestTrayIngestRejectsBadPayload(t *testing.T) {
	s := newTestServer(t)
	code, _ := postSweep(t, s, "/api/profile/readings", map[string]any{"at": 1, "readings": []column.TrayReading{{Tray: 999, Pressure: 1, Temperature: 1}}})
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range tray, got %d", code)
	}
}
