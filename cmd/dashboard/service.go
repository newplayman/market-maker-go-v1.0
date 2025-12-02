package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// TradeEvent represents a parsed trade event
type TradeEvent struct {
	Time      time.Time `json:"time"`
	Type      string    `json:"type"` // PLACE, CANCEL, FILL, RISK
	Symbol    string    `json:"symbol"`
	Price     float64   `json:"price,omitempty"`
	Quantity  float64   `json:"quantity,omitempty"`
	Side      string    `json:"side,omitempty"`
	Message   string    `json:"message"`
	Cost      float64   `json:"cost,omitempty"`       // For grinding stats
	RiskEvent string    `json:"risk_event,omitempty"` // For risk stats
}

// SystemStats holds aggregated statistics
type SystemStats struct {
	ActiveOrders     int       `json:"active_orders"`
	TotalPlaced      int       `json:"total_placed"`
	TotalCanceled    int       `json:"total_canceled"`
	TotalFilled      int       `json:"total_filled"`
	OrdersPerMin     float64   `json:"orders_per_min"`
	RiskTriggerCount int       `json:"risk_trigger_count"`
	GrindingCount    int       `json:"grinding_count"`
	GrindingSaved    float64   `json:"grinding_saved"`
	StartTime        time.Time `json:"start_time"`

	// Financials
	NetValue      float64 `json:"net_value"`
	TotalPNL      float64 `json:"total_pnl"`
	UnrealizedPNL float64 `json:"unrealized_pnl"`
	PositionSize  float64 `json:"position_size"`
	EntryPrice    float64 `json:"entry_price"`
	CurrentPrice  float64 `json:"current_price"`
	InitialPrice  float64 `json:"initial_price"`
}

// DashboardService manages the backend logic
type DashboardService struct {
	mu           sync.RWMutex
	logPath      string
	stats        SystemStats
	recentEvents []TradeEvent
	isRunning    bool
	pid          int
	db           *DB
}

func NewDashboardService(logPath string) *DashboardService {
	db, err := NewDB("dashboard.db")
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize database")
	}

	return &DashboardService{
		logPath: logPath,
		stats: SystemStats{
			StartTime: time.Now(),
		},
		recentEvents: make([]TradeEvent, 0, 100),
		db:           db,
	}
}

// StartLogWatcher starts tailing the log file
func (s *DashboardService) StartLogWatcher() {
	go func() {
		// Wait for file to exist
		for {
			if _, err := os.Stat(s.logPath); err == nil {
				break
			}
			time.Sleep(1 * time.Second)
		}

		file, err := os.Open(s.logPath)
		if err != nil {
			log.Error().Err(err).Msg("Failed to open log file")
			return
		}
		defer file.Close()

		// Seek to end - REMOVED to ensure we catch startup logs
		// file.Seek(0, io.SeekEnd)

		log.Info().Str("path", s.logPath).Msg("Log watcher started")
		reader := bufio.NewReader(file)

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				log.Error().Err(err).Msg("Error reading log file")
				break
			}
			// log.Info().Str("line", line).Msg("Read line") // Too noisy
			s.parseLine(line)

		}
	}()
}

// parseLine parses a log line and updates stats
func (s *DashboardService) parseLine(line string) {
	// log.Info().Str("line", line).Msg("Parsing line")
	// if strings.Contains(line, "TICKER_EVENT") {
	// 	log.Info().Msg("Matched TICKER_EVENT")
	// }
	s.mu.Lock()

	defer s.mu.Unlock()

	ts := time.Now() // Default to now if parse fails
	parts := strings.Split(line, " ")
	if len(parts) > 0 {
		if t, err := time.Parse(time.RFC3339, parts[0]); err == nil {
			ts = t
		}
	}

	event := TradeEvent{Time: ts, Message: line}

	if strings.Contains(line, "下单成功") {
		event.Type = "PLACE"
		s.stats.TotalPlaced++
		s.extractOrderDetails(line, &event)
	} else if strings.Contains(line, "撤单成功") {
		event.Type = "CANCEL"
		s.stats.TotalCanceled++
		s.extractOrderDetails(line, &event)
	} else if strings.Contains(line, "成交") { // Assuming "成交" or similar for fills
		event.Type = "FILL"
		s.stats.TotalFilled++
		s.extractOrderDetails(line, &event)
	} else if strings.Contains(line, "WRN") || strings.Contains(line, "ERR") {
		event.Type = "RISK"
		s.stats.RiskTriggerCount++
		event.RiskEvent = line // Store full line as risk detail
	} else if strings.Contains(line, "TRADE_EVENT") {
		// Parse structured trade data
		startIdx := strings.Index(line, "trade_data=")
		if startIdx != -1 {
			re := regexp.MustCompile(`trade_data=.*?({.*})`)
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				var tradeData map[string]interface{}
				if err := json.Unmarshal([]byte(matches[1]), &tradeData); err == nil {
					// Insert into DB
					if s.db != nil {
						symbol, _ := tradeData["symbol"].(string)
						side, _ := tradeData["side"].(string)
						price, _ := tradeData["price"].(float64)
						quantity, _ := tradeData["quantity"].(float64)
						pnl, _ := tradeData["pnl"].(float64)
						timestamp, _ := tradeData["timestamp"].(float64)

						s.db.InsertTrade(symbol, side, price, quantity, pnl, int64(timestamp))
					}
				}
			}
		}
	} else if strings.Contains(line, "TICKER_EVENT") {
		// ... existing TICKER_EVENT logic ...
		startIdx := strings.Index(line, "ticker_data=")
		if startIdx != -1 {
			re := regexp.MustCompile(`ticker_data=.*?({.*})`)
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				// log.Info().Str("json", matches[1]).Msg("Extracted JSON")
				var tickerData map[string]interface{}
				if err := json.Unmarshal([]byte(matches[1]), &tickerData); err == nil {
					s.updateStatsFromTicker(tickerData)
					// log.Info().Interface("stats", s.stats).Msg("Updated stats")
				} else {
					log.Error().Err(err).Str("data", matches[1]).Msg("Failed to unmarshal ticker data")
				}
			} else {
				log.Warn().Str("line", line).Msg("Regex failed to match ticker_data")
			}
		} else {
			log.Warn().Str("line", line).Msg("Could not find ticker_data= prefix")
		}
	} else if strings.Contains(line, "active_orders=") {
		// Parse active orders count from log like: active_orders=36
		re := regexp.MustCompile(`active_orders=(\d+)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			if count, err := strconv.Atoi(matches[1]); err == nil {
				s.stats.ActiveOrders = count
			}
		}
	}

	if event.Type != "" {
		s.recentEvents = append(s.recentEvents, event)
		if len(s.recentEvents) > 100 {
			s.recentEvents = s.recentEvents[1:] // Keep last 100
		}
	}
}

func (s *DashboardService) updateStatsFromTicker(data map[string]interface{}) {
	if val, ok := data["active_orders"].(float64); ok {
		s.stats.ActiveOrders = int(val)
	}
	if val, ok := data["mid_price"].(float64); ok {
		s.stats.CurrentPrice = val
		if s.stats.InitialPrice == 0 {
			s.stats.InitialPrice = val
		}
	}
	if val, ok := data["position"].(float64); ok {
		s.stats.PositionSize = val
	}
	if val, ok := data["entry_price"].(float64); ok {
		s.stats.EntryPrice = val
	}
	if val, ok := data["unrealized_pnl"].(float64); ok {
		s.stats.UnrealizedPNL = val
	}
	if val, ok := data["total_pnl"].(float64); ok {
		s.stats.TotalPNL = val
	}
	if val, ok := data["net_value"].(float64); ok {
		s.stats.NetValue = val
	} else {
		// Fallback if not present (should not happen with new runner)
		s.stats.NetValue = 10000 + s.stats.TotalPNL
	}

	// Insert snapshot into DB
	if s.db != nil {
		// Rate limit snapshots if needed, but for now every second is fine
		walletBalance := 0.0
		if val, ok := data["wallet_balance"].(float64); ok {
			walletBalance = val
		}
		s.db.InsertSnapshot(s.stats.NetValue, s.stats.TotalPNL, walletBalance)
	}
}

func (s *DashboardService) extractOrderDetails(line string, event *TradeEvent) {
	// Extract key-value pairs like price=123 qty=0.1
	rePrice := regexp.MustCompile(`price=([\d\.]+)`)
	reQty := regexp.MustCompile(`qty=([\d\.]+)`)
	reSide := regexp.MustCompile(`side=(\w+)`)
	reSymbol := regexp.MustCompile(`symbol=(\w+)`)

	if match := rePrice.FindStringSubmatch(line); len(match) > 1 {
		event.Price, _ = strconv.ParseFloat(match[1], 64)
	}
	if match := reQty.FindStringSubmatch(line); len(match) > 1 {
		event.Quantity, _ = strconv.ParseFloat(match[1], 64)
	}
	if match := reSide.FindStringSubmatch(line); len(match) > 1 {
		event.Side = match[1]
	}
	if match := reSymbol.FindStringSubmatch(line); len(match) > 1 {
		event.Symbol = match[1]
	}
}

// GetStats returns current stats
func (s *DashboardService) GetStats() SystemStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Calculate orders per minute
	duration := time.Since(s.stats.StartTime).Minutes()
	if duration > 0 {
		s.stats.OrdersPerMin = float64(s.stats.TotalPlaced+s.stats.TotalCanceled) / duration
	}

	return s.stats
}

// GetRecentEvents returns recent events
func (s *DashboardService) GetRecentEvents() []TradeEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy
	events := make([]TradeEvent, len(s.recentEvents))
	copy(events, s.recentEvents)
	return events
}

// CheckProcessStatus checks if phoenix is running
func (s *DashboardService) CheckProcessStatus() (bool, int) {
	cmd := exec.Command("pgrep", "-f", "phoenix")
	output, err := cmd.Output()
	if err != nil {
		s.isRunning = false
		s.pid = 0
		return false, 0
	}
	pidStr := strings.TrimSpace(string(output))
	if pid, err := strconv.Atoi(strings.Split(pidStr, "\n")[0]); err == nil {
		s.isRunning = true
		s.pid = pid
		return true, pid
	}
	return false, 0
}

// StartProcess starts the trading bot
func (s *DashboardService) StartProcess() error {
	if s.isRunning {
		return fmt.Errorf("process already running")
	}
	cmd := exec.Command("./scripts/start_live.sh")
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

// StopProcess stops the trading bot
func (s *DashboardService) StopProcess() error {
	cmd := exec.Command("./scripts/stop_live.sh")
	return cmd.Run()
}
