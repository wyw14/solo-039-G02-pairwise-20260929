package api

import (
	"net/http"
	"time"
)

func (s *Server) ingestTrays(w http.ResponseWriter, r *http.Request) {
	var input traySweepRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.At == 0 {
		input.At = time.Now().Unix()
	}
	diagnosis, err := s.plant.IngestTrays(input.At, input.Readings, input.Expected)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, diagnosis)
}

func (s *Server) learnTrays(w http.ResponseWriter, r *http.Request) {
	var input traySweepRequest
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.At == 0 {
		input.At = time.Now().Unix()
	}
	count, err := s.plant.LearnTrays(input.At, input.Readings, input.Expected)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"learned_trays": count})
}

func (s *Server) trayDiagnosis(w http.ResponseWriter, r *http.Request) {
	diagnosis, ok := s.plant.LastTrayDiagnosis()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no tray sweep received yet"})
		return
	}
	writeJSON(w, http.StatusOK, diagnosis)
}
