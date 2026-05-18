package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileRejectsMalformedYAML(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, "server:\n  listen_addr: [")

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, validConfigYAML(t.TempDir())+"\nunknown: true\n")

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsMissingDatabasePath(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database: {}
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsInvalidDelay(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  default_delay_ms: -1
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsMissingDelay(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsOtherMissingRealizationDelays(t *testing.T) {
	t.Parallel()

	for name, realization := range map[string]string{
		"create": `
  default_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0`,
		"update": `
  default_delay_ms: 0
  create_delay_ms: 0
  delete_delay_ms: 0`,
		"delete": `
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0`,
	} {
		configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi-`+name+`.db"
realization:`+realization+`
search:
  default_page_size: 1000
  max_page_size: 1000
`)

		_, err := LoadFile(configPath)
		if err == nil {
			t.Fatalf("%s missing delay LoadFile() error = nil, want error", name)
		}
	}
}

func TestLoadFileRejectsInvalidListenAddress(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsInvalidListenPorts(t *testing.T) {
	t.Parallel()

	for name, listenAddr := range map[string]string{
		"missing port": "127.0.0.1:",
		"non numeric":  "127.0.0.1:not-a-port",
		"out of range": "127.0.0.1:70000",
	} {
		configPath := writeConfigTestFile(t, `
server:
  listen_addr: "`+listenAddr+`"
database:
  path: "/tmp/nsx-t-mockapi-`+name+`.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`)

		_, err := LoadFile(configPath)
		if err == nil {
			t.Fatalf("%s LoadFile() error = nil, want error", name)
		}
	}
}

func TestLoadFileRejectsNonPositiveSearchPageSize(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 0
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsMissingSearchPageSizes(t *testing.T) {
	t.Parallel()

	for name, search := range map[string]string{
		"default": `
  max_page_size: 1000`,
		"max": `
  default_page_size: 1000`,
	} {
		configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi-search-`+name+`.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:`+search+`
`)

		_, err := LoadFile(configPath)
		if err == nil {
			t.Fatalf("%s missing search size LoadFile() error = nil, want error", name)
		}
	}
}

func TestLoadFileRejectsNonPositiveSearchMaxPageSize(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi-search-max.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 0
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsInvalidKindDelay(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
  kind_delay_ms:
    Group: -1
search:
  default_page_size: 1000
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileRejectsInvalidSearchPageSizes(t *testing.T) {
	t.Parallel()

	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "/tmp/nsx-t-mockapi.db"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1001
  max_page_size: 1000
`)

	_, err := LoadFile(configPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func TestLoadFileReturnsTrustedConfig(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "data", "nsx-t-mockapi.db")
	configPath := writeConfigTestFile(t, `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "`+dbPath+`"
realization:
  default_delay_ms: 10
  create_delay_ms: 20
  update_delay_ms: 30
  delete_delay_ms: 40
  kind_delay_ms:
    Group: 50
search:
  default_page_size: 500
  max_page_size: 1000
`)

	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Server.ListenAddr != "127.0.0.1:0" {
		t.Fatalf("ListenAddr = %q, want 127.0.0.1:0", cfg.Server.ListenAddr)
	}
	if cfg.Database.Path != dbPath {
		t.Fatalf("Database.Path = %q, want %q", cfg.Database.Path, dbPath)
	}
	if cfg.Realization.DefaultDelayMS != 10 {
		t.Fatalf("DefaultDelayMS = %d, want 10", cfg.Realization.DefaultDelayMS)
	}
	if cfg.Realization.KindDelayMS["Group"] != 50 {
		t.Fatalf("KindDelayMS[Group] = %d, want 50", cfg.Realization.KindDelayMS["Group"])
	}
	if cfg.Search.DefaultPageSize != 500 {
		t.Fatalf("DefaultPageSize = %d, want 500", cfg.Search.DefaultPageSize)
	}
}

func TestLoadFileRejectsMissingPath(t *testing.T) {
	t.Parallel()

	_, err := LoadFile("")
	if err == nil {
		t.Fatal("LoadFile() error = nil, want error")
	}
}

func validConfigYAML(tempDir string) string {
	return `
server:
  listen_addr: "127.0.0.1:0"
database:
  path: "` + filepath.Join(tempDir, "data", "nsx-t-mockapi.db") + `"
realization:
  default_delay_ms: 0
  create_delay_ms: 0
  update_delay_ms: 0
  delete_delay_ms: 0
search:
  default_page_size: 1000
  max_page_size: 1000
`
}

func writeConfigTestFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
