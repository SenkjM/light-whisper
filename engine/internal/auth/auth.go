// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

// Package auth implements root / admin / user bearer tokens and stats
// persistence for the management backend (PLAN §11.4).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Role of a bearer token.
type Role string

const (
	RoleRoot  Role = "root"
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Capability is a coarse permission gate.
type Capability string

const (
	CapInvokeASR       Capability = "invoke.asr"
	CapInvokeJobs      Capability = "invoke.jobs"
	CapEngineControl   Capability = "engine.control"
	CapConfigRead      Capability = "config.read"
	CapConfigWrite     Capability = "config.write"
	CapStatsRead       Capability = "stats.read"
	CapTokenList       Capability = "token.list"
	CapTokenManage     Capability = "token.manage"
	CapAdminManage     Capability = "admin.manage"
)

var roleCaps = map[Role][]Capability{
	RoleRoot: {
		CapInvokeASR, CapInvokeJobs, CapEngineControl,
		CapConfigRead, CapConfigWrite, CapStatsRead,
		CapTokenList, CapTokenManage, CapAdminManage,
	},
	RoleAdmin: {
		CapInvokeASR, CapInvokeJobs, CapEngineControl,
		CapConfigRead, CapConfigWrite, CapStatsRead,
		CapTokenList, CapTokenManage, CapAdminManage,
	},
	RoleUser: {
		CapInvokeASR, CapInvokeJobs, CapConfigRead, CapStatsRead,
	},
}

// TokenMeta is safe to return to admin (never includes plaintext or root secret).
type TokenMeta struct {
	ID        string    `json:"id"`
	Role      Role      `json:"role"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  *time.Time `json:"last_used,omitempty"`
	// Masked is always true for root; for others may expose prefix for UX.
	Masked    bool   `json:"masked"`
	Prefix    string `json:"prefix,omitempty"`
}

// Stats are process-lifetime counters persisted periodically.
type Stats struct {
	ASRStreams      int64 `json:"asr_streams"`
	Transcribes     int64 `json:"transcribes"`
	JobsSubmitted   int64 `json:"jobs_submitted"`
	JobsCompleted   int64 `json:"jobs_completed"`
	JobsFailed      int64 `json:"jobs_failed"`
	HTTPRequests    int64 `json:"http_requests"`
	Unauthorized    int64 `json:"unauthorized"`
}

// Store holds SQLite-backed tokens + in-memory root plaintext.
type Store struct {
	mu         sync.Mutex
	db         *sql.DB
	rootPlain  string
	rootID     string
	stats      Stats
}

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrImmutable    = errors.New("immutable_root")
)

// Open opens (or creates) the auth database under dataDir and installs a fresh root token.
func Open(dbPath string) (*Store, string, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	root, err := s.rotateRoot()
	if err != nil {
		_ = db.Close()
		return nil, "", err
	}
	_ = s.loadStats()
	return s, root, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.flushStatsLocked()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// RootToken returns the plaintext root token for the spawning shell only.
func (s *Store) RootToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rootPlain
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tokens (
  id TEXT PRIMARY KEY,
  role TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  hash TEXT NOT NULL,
  prefix TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_used INTEGER
);
CREATE TABLE IF NOT EXISTS stats (
  key TEXT PRIMARY KEY,
  value INTEGER NOT NULL
);
`)
	return err
}

func (s *Store) rotateRoot() (string, error) {
	plain, err := randomToken()
	if err != nil {
		return "", err
	}
	id := "root"
	hash := hashToken(plain)
	prefix := plain[:8]
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM tokens WHERE role = ?`, RoleRoot)
	_, err = s.db.Exec(
		`INSERT INTO tokens(id, role, label, hash, prefix, created_at) VALUES(?,?,?,?,?,?)`,
		id, RoleRoot, "root", hash, prefix, now,
	)
	if err != nil {
		return "", err
	}
	s.rootPlain = plain
	s.rootID = id
	return plain, nil
}

// Authenticate validates a bearer token and returns its role.
func (s *Store) Authenticate(bearer string) (Role, string, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		s.AddStat("unauthorized", 1)
		return "", "", ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(bearer), []byte(s.rootPlain)) == 1 {
		now := time.Now().Unix()
		_, _ = s.db.Exec(`UPDATE tokens SET last_used=? WHERE id=?`, now, s.rootID)
		return RoleRoot, s.rootID, nil
	}
	h := hashToken(bearer)
	var id string
	var role Role
	err := s.db.QueryRow(`SELECT id, role FROM tokens WHERE hash=? AND role != ?`, h, RoleRoot).Scan(&id, &role)
	if err != nil {
		s.stats.Unauthorized++
		return "", "", ErrUnauthorized
	}
	now := time.Now().Unix()
	_, _ = s.db.Exec(`UPDATE tokens SET last_used=? WHERE id=?`, now, id)
	return role, id, nil
}

// Has reports whether role includes cap.
func Has(role Role, cap Capability) bool {
	for _, c := range roleCaps[role] {
		if c == cap {
			return true
		}
	}
	return false
}

// Capabilities returns the list for a role.
func Capabilities(role Role) []Capability {
	out := append([]Capability(nil), roleCaps[role]...)
	return out
}

// ListTokens returns metadata; root secret is never included.
func (s *Store) ListTokens() ([]TokenMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, role, label, prefix, created_at, last_used FROM tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenMeta
	for rows.Next() {
		var m TokenMeta
		var created int64
		var last sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Role, &m.Label, &m.Prefix, &created, &last); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(created, 0).UTC()
		if last.Valid {
			t := time.Unix(last.Int64, 0).UTC()
			m.LastUsed = &t
		}
		if m.Role == RoleRoot {
			m.Masked = true
			m.Prefix = "" // admin must not see root prefix either that could help brute — keep empty
		} else {
			m.Masked = false
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateToken mints an admin or user token. Returns plaintext once.
func (s *Store) CreateToken(role Role, label string) (id, plain string, err error) {
	if role != RoleAdmin && role != RoleUser {
		return "", "", fmt.Errorf("invalid role")
	}
	plain, err = randomToken()
	if err != nil {
		return "", "", err
	}
	id = "tok_" + hex.EncodeToString(randomBytes(8))
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(
		`INSERT INTO tokens(id, role, label, hash, prefix, created_at) VALUES(?,?,?,?,?,?)`,
		id, role, label, hashToken(plain), plain[:8], time.Now().Unix(),
	)
	return id, plain, err
}

// DeleteToken removes a non-root token.
func (s *Store) DeleteToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.rootID || id == "root" {
		return ErrImmutable
	}
	res, err := s.db.Exec(`DELETE FROM tokens WHERE id=? AND role != ?`, id, RoleRoot)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddStat(key string, delta int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch key {
	case "asr_streams":
		s.stats.ASRStreams += delta
	case "transcribes":
		s.stats.Transcribes += delta
	case "jobs_submitted":
		s.stats.JobsSubmitted += delta
	case "jobs_completed":
		s.stats.JobsCompleted += delta
	case "jobs_failed":
		s.stats.JobsFailed += delta
	case "http_requests":
		s.stats.HTTPRequests += delta
	case "unauthorized":
		s.stats.Unauthorized += delta
	}
}

func (s *Store) SnapshotStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.flushStatsLocked()
	return s.stats
}

func (s *Store) loadStats() error {
	rows, err := s.db.Query(`SELECT key, value FROM stats`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		switch k {
		case "asr_streams":
			s.stats.ASRStreams = v
		case "transcribes":
			s.stats.Transcribes = v
		case "jobs_submitted":
			s.stats.JobsSubmitted = v
		case "jobs_completed":
			s.stats.JobsCompleted = v
		case "jobs_failed":
			s.stats.JobsFailed = v
		case "http_requests":
			s.stats.HTTPRequests = v
		case "unauthorized":
			s.stats.Unauthorized = v
		}
	}
	return rows.Err()
}

func (s *Store) flushStatsLocked() error {
	pairs := []struct {
		k string
		v int64
	}{
		{"asr_streams", s.stats.ASRStreams},
		{"transcribes", s.stats.Transcribes},
		{"jobs_submitted", s.stats.JobsSubmitted},
		{"jobs_completed", s.stats.JobsCompleted},
		{"jobs_failed", s.stats.JobsFailed},
		{"http_requests", s.stats.HTTPRequests},
		{"unauthorized", s.stats.Unauthorized},
	}
	for _, p := range pairs {
		if _, err := s.db.Exec(
			`INSERT INTO stats(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			p.k, p.v,
		); err != nil {
			return err
		}
	}
	return nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	b := randomBytes(32)
	return hex.EncodeToString(b), nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
