package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
)

type APIHandler struct {
	service *DashboardService
}

func NewAPIHandler(s *DashboardService) *APIHandler {
	return &APIHandler{service: s}
}

func (h *APIHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := h.service.GetStats()
	json.NewEncoder(w).Encode(stats)
}

func (h *APIHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	events := h.service.GetRecentEvents()
	json.NewEncoder(w).Encode(events)
}

func (h *APIHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	running, pid := h.service.CheckProcessStatus()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running": running,
		"pid":     pid,
	})
}

func (h *APIHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.service.StartProcess(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *APIHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.service.StopProcess(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *APIHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	configPath := "configs/phoenix_live.yaml"
	if r.Method == http.MethodGet {
		content, err := os.ReadFile(configPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		w.Write(content)
	} else if r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := os.WriteFile(configPath, body, 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *APIHandler) HandleHistoryTrades(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil {
			limit = val
		}
	}

	if h.service.db == nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	trades, err := h.service.db.GetRecentTrades(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(trades)
}

func (h *APIHandler) HandleHistorySnapshots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil {
			limit = val
		}
	}

	if h.service.db == nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	snapshots, err := h.service.db.GetSnapshots(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(snapshots)
}
