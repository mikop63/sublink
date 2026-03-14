package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Config struct {
	Server   ServerConfig   `json:"server"`
	Upstream UpstreamConfig `json:"upstream"`
	Admin    AdminConfig    `json:"admin"`
}

type ServerConfig struct {
	Port int `json:"port"`
}

type UpstreamConfig struct {
	TimeoutSec     int      `json:"timeout_sec"`
	UpdateInterval int      `json:"update_interval"`
	Hosts          []string `json:"hosts"`
}

type AdminConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (u UpstreamConfig) Timeout() time.Duration {
	if u.TimeoutSec <= 0 {
		return 15 * time.Second
	}
	return time.Duration(u.TimeoutSec) * time.Second
}

// Manager wraps Config with a mutex for safe hot-reload from the admin panel.
type Manager struct {
	mu   sync.RWMutex
	cfg  *Config
	path string
}

func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Save validates, writes to disk, and replaces in-memory config.
// Uses direct WriteFile instead of tmp+rename — rename across Docker
// bind-mount boundaries fails with "device or resource busy".
func (m *Manager) Save(cfg *Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	cfg.applyDefaults()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(m.path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", m.path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	cfg.applyDefaults()

	m.mu.Lock()
	m.cfg = &cfg
	m.mu.Unlock()
	return nil
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Upstream.TimeoutSec == 0 {
		c.Upstream.TimeoutSec = 15
	}
	if c.Upstream.UpdateInterval == 0 {
		c.Upstream.UpdateInterval = 1
	}
}

func (c *Config) validate() error {
	if len(c.Upstream.Hosts) == 0 {
		return fmt.Errorf("upstream.hosts must not be empty")
	}
	if c.Admin.Username == "" || c.Admin.Password == "" {
		return fmt.Errorf("admin.username and admin.password are required")
	}
	return nil
}