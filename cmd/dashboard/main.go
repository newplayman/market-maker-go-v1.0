package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	port := flag.Int("port", 8081, "Dashboard port")
	logPath := flag.String("log", "logs/phoenix_live.out", "Path to log file")
	flag.Parse()

	// Setup logger
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Initialize service
	service := NewDashboardService(*logPath)
	service.StartLogWatcher()

	// Initialize API
	api := NewAPIHandler(service)

	// Setup routes
	http.HandleFunc("/api/stats", api.HandleStats)
	http.HandleFunc("/api/events", api.HandleEvents)
	http.HandleFunc("/api/status", api.HandleStatus)
	http.HandleFunc("/api/start", api.HandleStart)
	http.HandleFunc("/api/stop", api.HandleStop)
	http.HandleFunc("/api/config", api.HandleConfig)
	http.HandleFunc("/api/history/trades", api.HandleHistoryTrades)
	http.HandleFunc("/api/history/snapshots", api.HandleHistorySnapshots)

	// Serve static files
	fs := http.FileServer(http.Dir("cmd/dashboard/static"))
	http.Handle("/", fs)

	log.Info().Int("port", *port).Msg("Starting dashboard server")
	addr := fmt.Sprintf(":%d", *port)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal().Err(err).Msg("Server failed")
	}
}
