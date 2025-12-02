package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
)

type DB struct {
	conn *sql.DB
}

func NewDB(path string) (*DB, error) {
	log.Info().Str("path", path).Msg("Initializing database")
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	log.Info().Msg("Database connection established")

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		return nil, err
	}

	return db, nil
}

func (db *DB) initSchema() error {
	// Create trades table
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS trades (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT,
			side TEXT,
			price REAL,
			quantity REAL,
			pnl REAL,
			timestamp INTEGER
		);
	`)
	if err != nil {
		return fmt.Errorf("create trades table: %w", err)
	}

	// Create snapshots table for historical charts
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp INTEGER,
			net_value REAL,
			total_pnl REAL,
			wallet_balance REAL
		);
	`)
	if err != nil {
		return fmt.Errorf("create snapshots table: %w", err)
	}

	return nil
}

func (db *DB) InsertTrade(symbol, side string, price, quantity, pnl float64, timestamp int64) error {
	_, err := db.conn.Exec(`
		INSERT INTO trades (symbol, side, price, quantity, pnl, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
	`, symbol, side, price, quantity, pnl, timestamp)
	return err
}

func (db *DB) InsertSnapshot(netValue, totalPNL, walletBalance float64) error {
	_, err := db.conn.Exec(`
		INSERT INTO snapshots (timestamp, net_value, total_pnl, wallet_balance)
		VALUES (?, ?, ?, ?)
	`, time.Now().Unix(), netValue, totalPNL, walletBalance)
	return err
}

type TradeRecord struct {
	ID        int64   `json:"id"`
	Symbol    string  `json:"symbol"`
	Side      string  `json:"side"`
	Price     float64 `json:"price"`
	Quantity  float64 `json:"quantity"`
	PNL       float64 `json:"pnl"`
	Timestamp int64   `json:"timestamp"`
}

func (db *DB) GetRecentTrades(limit int) ([]TradeRecord, error) {
	rows, err := db.conn.Query(`
		SELECT id, symbol, side, price, quantity, pnl, timestamp
		FROM trades
		ORDER BY timestamp DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []TradeRecord
	for rows.Next() {
		var t TradeRecord
		if err := rows.Scan(&t.ID, &t.Symbol, &t.Side, &t.Price, &t.Quantity, &t.PNL, &t.Timestamp); err != nil {
			return nil, err
		}
		trades = append(trades, t)
	}
	return trades, nil
}

type SnapshotRecord struct {
	Timestamp     int64   `json:"timestamp"`
	NetValue      float64 `json:"net_value"`
	TotalPNL      float64 `json:"total_pnl"`
	WalletBalance float64 `json:"wallet_balance"`
}

func (db *DB) GetSnapshots(limit int) ([]SnapshotRecord, error) {
	rows, err := db.conn.Query(`
		SELECT timestamp, net_value, total_pnl, wallet_balance
		FROM snapshots
		ORDER BY timestamp ASC
		LIMIT ?
	`, limit) // Note: usually we want a range, but limit is fine for now
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []SnapshotRecord
	for rows.Next() {
		var s SnapshotRecord
		if err := rows.Scan(&s.Timestamp, &s.NetValue, &s.TotalPNL, &s.WalletBalance); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}
