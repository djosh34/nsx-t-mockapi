// Package config loads and validates the explicit startup YAML configuration.
//
//nolint:tagliatelle // The config file contract intentionally uses snake_case YAML keys.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

const maxTCPPort = 65535

var (
	errConfigPathRequired      = errors.New("config path is required")
	errListenAddrRequired      = errors.New("server.listen_addr is required")
	errDatabasePathRequired    = errors.New("database.path is required")
	errSearchPageSizeInvalid   = errors.New("search page size is invalid")
	errListenPortRequired      = errors.New("server.listen_addr port is required")
	errListenPortRangeInvalid  = errors.New("server.listen_addr port exceeds allowed range")
	errFieldRequired           = errors.New("config field is required")
	errFieldNegative           = errors.New("config field must be >= 0")
	errFieldNotPositive        = errors.New("config field must be > 0")
	errKindDelayNameRequired   = errors.New("realization.kind_delay_ms kind name is required")
	errListenAddrFormatInvalid = errors.New("server.listen_addr must be host:port")
)

// Config is the trusted application configuration after startup validation.
type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Realization RealizationConfig
	Search      SearchConfig
}

// ServerConfig configures the HTTP listener.
type ServerConfig struct {
	ListenAddr string
}

// DatabaseConfig configures SQLite persistence.
type DatabaseConfig struct {
	Path string
}

// RealizationConfig configures deterministic delayed-realization timing.
type RealizationConfig struct {
	DefaultDelayMS int
	CreateDelayMS  int
	UpdateDelayMS  int
	DeleteDelayMS  int
	KindDelayMS    map[string]int
}

// SearchConfig configures search pagination limits.
type SearchConfig struct {
	DefaultPageSize int
	MaxPageSize     int
}

type rawConfig struct {
	Server      rawServerConfig      `yaml:"server"`
	Database    rawDatabaseConfig    `yaml:"database"`
	Realization rawRealizationConfig `yaml:"realization"`
	Search      rawSearchConfig      `yaml:"search"`
}

type rawServerConfig struct {
	ListenAddr string `yaml:"listen_addr"`
}

type rawDatabaseConfig struct {
	Path string `yaml:"path"`
}

type rawRealizationConfig struct {
	DefaultDelayMS *int            `yaml:"default_delay_ms"`
	CreateDelayMS  *int            `yaml:"create_delay_ms"`
	UpdateDelayMS  *int            `yaml:"update_delay_ms"`
	DeleteDelayMS  *int            `yaml:"delete_delay_ms"`
	KindDelayMS    map[string]*int `yaml:"kind_delay_ms"`
}

type rawSearchConfig struct {
	DefaultPageSize *int `yaml:"default_page_size"`
	MaxPageSize     *int `yaml:"max_page_size"`
}

// LoadFile reads path once, rejects unknown YAML fields, and validates a trusted Config.
func LoadFile(path string) (Config, error) {
	if path == "" {
		return Config{}, errConfigPathRequired
	}

	//nolint:gosec // The explicit config path is the intended input to this startup boundary.
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file %q: %w", path, err)
	}

	var raw rawConfig
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err = decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode config file %q: %w", path, err)
	}

	cfg, err := validate(raw)
	if err != nil {
		return Config{}, fmt.Errorf("validate config file %q: %w", path, err)
	}

	return cfg, nil
}

func validate(raw rawConfig) (Config, error) {
	if raw.Server.ListenAddr == "" {
		return Config{}, errListenAddrRequired
	}
	if err := validateListenAddr(raw.Server.ListenAddr); err != nil {
		return Config{}, err
	}
	if raw.Database.Path == "" {
		return Config{}, errDatabasePathRequired
	}

	realization, err := validateRealization(raw.Realization)
	if err != nil {
		return Config{}, err
	}
	search, err := validateSearch(raw.Search)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Server: ServerConfig{
			ListenAddr: raw.Server.ListenAddr,
		},
		Database: DatabaseConfig{
			Path: raw.Database.Path,
		},
		Realization: realization,
		Search:      search,
	}, nil
}

func validateListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %w", errListenAddrFormatInvalid, err)
	}
	if port == "" {
		return errListenPortRequired
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return fmt.Errorf("%w: port %q: %w", errListenAddrFormatInvalid, port, err)
	}
	if portNumber > maxTCPPort {
		return fmt.Errorf("%w: %d", errListenPortRangeInvalid, portNumber)
	}
	return nil
}

func validateRealization(raw rawRealizationConfig) (RealizationConfig, error) {
	defaultDelay, err := requiredNonNegativeInt("realization.default_delay_ms", raw.DefaultDelayMS)
	if err != nil {
		return RealizationConfig{}, err
	}
	createDelay, err := requiredNonNegativeInt("realization.create_delay_ms", raw.CreateDelayMS)
	if err != nil {
		return RealizationConfig{}, err
	}
	updateDelay, err := requiredNonNegativeInt("realization.update_delay_ms", raw.UpdateDelayMS)
	if err != nil {
		return RealizationConfig{}, err
	}
	deleteDelay, err := requiredNonNegativeInt("realization.delete_delay_ms", raw.DeleteDelayMS)
	if err != nil {
		return RealizationConfig{}, err
	}
	kindDelay, err := validateKindDelays(raw.KindDelayMS)
	if err != nil {
		return RealizationConfig{}, err
	}

	return RealizationConfig{
		DefaultDelayMS: defaultDelay,
		CreateDelayMS:  createDelay,
		UpdateDelayMS:  updateDelay,
		DeleteDelayMS:  deleteDelay,
		KindDelayMS:    kindDelay,
	}, nil
}

func validateSearch(raw rawSearchConfig) (SearchConfig, error) {
	defaultPageSize, err := requiredPositiveInt("search.default_page_size", raw.DefaultPageSize)
	if err != nil {
		return SearchConfig{}, err
	}
	maxPageSize, err := requiredPositiveInt("search.max_page_size", raw.MaxPageSize)
	if err != nil {
		return SearchConfig{}, err
	}
	if defaultPageSize > maxPageSize {
		return SearchConfig{}, fmt.Errorf(
			"%w: default %d exceeds max %d",
			errSearchPageSizeInvalid,
			defaultPageSize,
			maxPageSize,
		)
	}

	return SearchConfig{
		DefaultPageSize: defaultPageSize,
		MaxPageSize:     maxPageSize,
	}, nil
}

func requiredNonNegativeInt(name string, value *int) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("%w: %s", errFieldRequired, name)
	}
	if *value < 0 {
		return 0, fmt.Errorf("%w: %s", errFieldNegative, name)
	}
	return *value, nil
}

func requiredPositiveInt(name string, value *int) (int, error) {
	if value == nil {
		return 0, fmt.Errorf("%w: %s", errFieldRequired, name)
	}
	if *value <= 0 {
		return 0, fmt.Errorf("%w: %s", errFieldNotPositive, name)
	}
	return *value, nil
}

func validateKindDelays(raw map[string]*int) (map[string]int, error) {
	if len(raw) == 0 {
		return map[string]int{}, nil
	}

	delays := make(map[string]int, len(raw))
	for kind, delay := range raw {
		if kind == "" {
			return nil, errKindDelayNameRequired
		}
		value, err := requiredNonNegativeInt("realization.kind_delay_ms."+kind, delay)
		if err != nil {
			return nil, err
		}
		delays[kind] = value
	}
	return delays, nil
}
