package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
)

var (
	errLoggerFailed = errors.New("logger failed")
	errWriteFailed  = errors.New("write failed")
)

type managerListJSON struct {
	Managers []managerJSON `json:"managers"`
	Count    int           `json:"count"`
}

type managerJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type userListJSON struct {
	Users []userJSON `json:"users"`
	Count int        `json:"count"`
}

type userJSON struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type userActionJSON struct {
	User   userJSON `json:"user"`
	Action string   `json:"action"`
}

func TestUserAddAndListJSONUseDataDirWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addManagerCode := realMainWithLogger(ctx, []string{"--data-dir", dataDir, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addManagerCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addManagerCode)
	}

	stdout.Reset()
	addUserCode := realMainWithLogger(
		ctx,
		[]string{
			"--data-dir", dataDir,
			"user", "add", "alice",
			"--manager", "nsx-t-1",
			"--password", "secret",
			"--read-write",
		},
		stdout,
		logger,
	)
	if addUserCode != 0 {
		t.Fatalf("user add exit code = %d, want 0", addUserCode)
	}

	stdout.Reset()
	listUserCode := realMainWithLogger(
		ctx,
		[]string{"--data-dir", dataDir, "user", "ls", "--manager", "nsx-t-1", "--output", "json"},
		stdout,
		logger,
	)
	if listUserCode != 0 {
		t.Fatalf("user ls exit code = %d, want 0", listUserCode)
	}

	if strings.Contains(stdout.String(), "secret") {
		t.Fatalf("user ls JSON leaked password in stdout: %s", stdout.String())
	}
	var got userListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("user ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	requireUserInList(t, got, "alice", string(sqlite.RoleReadWrite))
}

func TestUserRoleFlagsAndDeleteJSONUseDataDirWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	requireCLIExitZero(ctx, t, stdout, logger, "--data-dir", dataDir, "manager", "add", "nsx-t-1")
	addUserForTest(ctx, t, stdout, logger, "--data-dir", dataDir, "viewer", "--read-only")
	addUserForTest(ctx, t, stdout, logger, "--data-dir", dataDir, "writer", "")
	addUserForTest(ctx, t, stdout, logger, "--data-dir", dataDir, "operator", "--admin")

	stdout.Reset()
	listCode := realMainWithLogger(
		ctx,
		[]string{"--data-dir", dataDir, "user", "ls", "--manager", "nsx-t-1", "--output", "json"},
		stdout,
		logger,
	)
	if listCode != 0 {
		t.Fatalf("user ls exit code = %d, want 0", listCode)
	}
	var listed userListJSON
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("user ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	requireUserInList(t, listed, "viewer", string(sqlite.RoleReadOnly))
	requireUserInList(t, listed, "writer", string(sqlite.RoleReadWrite))
	requireUserInList(t, listed, "operator", string(sqlite.RoleAdmin))

	stdout.Reset()
	deleteCode := realMainWithLogger(
		ctx,
		[]string{"--data-dir", dataDir, "user", "delete", "writer", "--manager", "nsx-t-1", "--output", "json"},
		stdout,
		logger,
	)
	if deleteCode != 0 {
		t.Fatalf("user delete exit code = %d, want 0", deleteCode)
	}
	var deleted userActionJSON
	if err := json.Unmarshal(stdout.Bytes(), &deleted); err != nil {
		t.Fatalf("user delete stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if deleted.Action != "deleted" ||
		deleted.User.Username != "writer" ||
		deleted.User.Role != string(sqlite.RoleReadWrite) {
		t.Fatalf("user delete JSON = %+v, want deleted writer read-write", deleted)
	}

	stdout.Reset()
	requireCLIExitZero(ctx, t, stdout, logger,
		"--data-dir", dataDir, "user", "ls", "--manager", "nsx-t-1", "--output", "json")
	var afterDelete userListJSON
	if err := json.Unmarshal(stdout.Bytes(), &afterDelete); err != nil {
		t.Fatalf("user ls after delete stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	requireUserAbsent(t, afterDelete, "writer")
	requireUserInList(t, afterDelete, sqlite.DefaultAdminUsername, string(sqlite.RoleAdmin))
}

func TestUserCommandsUseManagerDirAndSingleDatabaseFileModesWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	managerDir := filepath.Join(t.TempDir(), "managers")
	requireCLIExitZero(ctx, t, stdout, logger, "--manager-dir", managerDir, "manager", "add", "nsx-t-1")
	addUserForTest(ctx, t, stdout, logger, "--manager-dir", managerDir, "managerdir-user", "--admin")
	stdout.Reset()
	requireCLIExitZero(ctx, t, stdout, logger,
		"--manager-dir", managerDir, "user", "delete", "managerdir-user", "--manager", "nsx-t-1")

	dbPath := filepath.Join(t.TempDir(), "single.db")
	stdout.Reset()
	requireCLIExitZero(ctx, t, stdout, logger,
		"--db", dbPath, "user", "add", "single-user", "--password", "secret", "--admin")
	stdout.Reset()
	requireCLIExitZero(ctx, t, stdout, logger, "--db", dbPath, "user", "ls", "--output", "json")
	var listed userListJSON
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		t.Fatalf("--db user ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	requireUserInList(t, listed, "single-user", string(sqlite.RoleAdmin))
}

func TestUserCommandValidationRejectsInvalidUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing manager in directory mode", args: []string{"user", "ls"}},
		{name: "unknown user command", args: []string{"user", "bogus"}},
		{name: "missing add username", args: []string{"user", "add", "--manager", "nsx-t-1"}},
		{name: "missing delete username", args: []string{"user", "delete", "--manager", "nsx-t-1"}},
		{name: "multiple roles", args: []string{"user", "add", "alice", "--manager", "nsx-t-1", "--read-only", "--admin"}},
		{name: "unsafe manager", args: []string{"user", "ls", "--manager", "nsx/t-1"}},
		{name: "unsafe username", args: []string{"user", "add", "bad/name", "--manager", "nsx-t-1"}},
		{name: "unknown output", args: []string{"user", "ls", "--manager", "nsx-t-1", "--output", "yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code := realMainWithLogger(ctx, tc.args, &bytes.Buffer{}, logger)
			if code == 0 {
				t.Fatalf("realMainWithLogger(%q) exit code = 0, want non-zero", tc.args)
			}
		})
	}
}

func TestManagerAddAndListJSONUseDataDirWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addCode := realMainWithLogger(ctx, []string{"--data-dir", dataDir, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addCode)
	}

	dbPath := filepath.Join(dataDir, "managers", "nsx-t-1", "nsx-t-mockapi.db")
	if !fileExists(t, dbPath) {
		t.Fatalf("manager database %q does not exist after add", dbPath)
	}

	stdout.Reset()
	listArgs := []string{"--data-dir", dataDir, "manager", "ls", "--output", "json"}
	listCode := realMainWithLogger(ctx, listArgs, stdout, logger)
	if listCode != 0 {
		t.Fatalf("manager ls exit code = %d, want 0", listCode)
	}

	var got managerListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("manager ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if got.Count != 1 {
		t.Fatalf("manager ls count = %d, want 1", got.Count)
	}
	if len(got.Managers) != 1 {
		t.Fatalf("manager ls returned %d managers, want 1", len(got.Managers))
	}
	if got.Managers[0].Name != "nsx-t-1" {
		t.Fatalf("manager name = %q, want nsx-t-1", got.Managers[0].Name)
	}
	if got.Managers[0].Path != dbPath {
		t.Fatalf("manager path = %q, want %q", got.Managers[0].Path, dbPath)
	}
}

func TestManagerClearRebootstrapsDatabaseAndRemovesResourcesWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addCode := realMainWithLogger(ctx, []string{"--data-dir", dataDir, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addCode)
	}
	dbPath := filepath.Join(dataDir, "managers", "nsx-t-1", "nsx-t-mockapi.db")
	createSegmentResource(ctx, t, dbPath)

	stdout.Reset()
	clearArgs := []string{"--data-dir", dataDir, "manager", "clear", "nsx-t-1", "--output", "json"}
	clearCode := realMainWithLogger(ctx, clearArgs, stdout, logger)
	if clearCode != 0 {
		t.Fatalf("manager clear exit code = %d, want 0", clearCode)
	}
	if !fileExists(t, dbPath) {
		t.Fatalf("manager database %q does not exist after clear", dbPath)
	}

	manager := openManagerForTest(ctx, t, "nsx-t-1", dbPath)
	defer closeManagerForTest(t, manager)
	_, found, err := sqlite.NewUserStore(manager.DB).FindUser(ctx, sqlite.DefaultAdminUsername)
	if err != nil {
		t.Fatalf("FindUser() after clear error = %v", err)
	}
	if !found {
		t.Fatalf("FindUser(%q) after clear found = false, want true", sqlite.DefaultAdminUsername)
	}
	resources, err := sqlite.NewResourceStore(manager.DB, sqlite.ResourceStoreOptions{}).List(ctx, sqlite.ListOptions{
		CollectionKey: "segments",
		ParentPath:    "/infra",
	})
	if err != nil {
		t.Fatalf("List() after clear error = %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("resource count after clear = %d, want 0", len(resources))
	}
}

func TestManagerDeleteRemovesDatabaseAndListEntryWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addCode := realMainWithLogger(ctx, []string{"--data-dir", dataDir, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addCode)
	}
	dbPath := filepath.Join(dataDir, "managers", "nsx-t-1", "nsx-t-mockapi.db")

	stdout.Reset()
	deleteArgs := []string{"--data-dir", dataDir, "manager", "delete", "nsx-t-1", "--output", "json"}
	deleteCode := realMainWithLogger(ctx, deleteArgs, stdout, logger)
	if deleteCode != 0 {
		t.Fatalf("manager delete exit code = %d, want 0", deleteCode)
	}
	if fileExists(t, dbPath) {
		t.Fatalf("manager database %q still exists after delete", dbPath)
	}
	for _, path := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if fileExists(t, path) {
			t.Fatalf("sqlite sidecar %q still exists after delete", path)
		}
	}

	stdout.Reset()
	listArgs := []string{"--data-dir", dataDir, "manager", "ls", "--output", "json"}
	listCode := realMainWithLogger(ctx, listArgs, stdout, logger)
	if listCode != 0 {
		t.Fatalf("manager ls exit code = %d, want 0", listCode)
	}
	var got managerListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("manager ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if got.Count != 0 {
		t.Fatalf("manager ls count after delete = %d, want 0", got.Count)
	}
	if len(got.Managers) != 0 {
		t.Fatalf("manager ls returned %d managers after delete, want 0", len(got.Managers))
	}
}

func TestManagerCommandsUseManagerDirModeWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	managerDir := filepath.Join(t.TempDir(), "managers")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addCode := realMainWithLogger(ctx, []string{"--manager-dir", managerDir, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addCode)
	}
	dbPath := filepath.Join(managerDir, "nsx-t-1", "nsx-t-mockapi.db")
	if !fileExists(t, dbPath) {
		t.Fatalf("manager database %q does not exist after manager-dir add", dbPath)
	}

	stdout.Reset()
	listArgs := []string{"--manager-dir", managerDir, "manager", "ls", "--output", "json"}
	listCode := realMainWithLogger(ctx, listArgs, stdout, logger)
	if listCode != 0 {
		t.Fatalf("manager ls exit code = %d, want 0", listCode)
	}
	var got managerListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("manager ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if got.Count != 1 || got.Managers[0].Path != dbPath {
		t.Fatalf("manager-dir list = %+v, want one manager at %q", got, dbPath)
	}

	stdout.Reset()
	deleteArgs := []string{"--manager-dir", managerDir, "manager", "delete", "nsx-t-1"}
	deleteCode := realMainWithLogger(ctx, deleteArgs, stdout, logger)
	if deleteCode != 0 {
		t.Fatalf("manager delete exit code = %d, want 0", deleteCode)
	}
	if fileExists(t, dbPath) {
		t.Fatalf("manager database %q still exists after manager-dir delete", dbPath)
	}
}

func TestManagerCommandsUseSingleDatabaseFileModeWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "single.db")
	stdout := &bytes.Buffer{}
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}

	addCode := realMainWithLogger(ctx, []string{"--db", dbPath, "manager", "add", "nsx-t-1"}, stdout, logger)
	if addCode != 0 {
		t.Fatalf("manager add exit code = %d, want 0", addCode)
	}
	if !fileExists(t, dbPath) {
		t.Fatalf("manager database %q does not exist after --db add", dbPath)
	}

	stdout.Reset()
	listCode := realMainWithLogger(ctx, []string{"--db", dbPath, "manager", "ls", "--output", "json"}, stdout, logger)
	if listCode != 0 {
		t.Fatalf("manager ls exit code = %d, want 0", listCode)
	}
	var got managerListJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("manager ls stdout is not valid JSON: %v\nstdout: %s", err, stdout.String())
	}
	if got.Count != 1 || got.Managers[0].Name != "nsx-t-1" || got.Managers[0].Path != dbPath {
		t.Fatalf("--db list = %+v, want nsx-t-1 at %q", got, dbPath)
	}

	runSingleDatabaseClearAndDelete(ctx, t, dbPath, stdout, logger)
}

func runSingleDatabaseClearAndDelete(
	ctx context.Context,
	t *testing.T,
	dbPath string,
	stdout *bytes.Buffer,
	logger func() (*zap.Logger, error),
) {
	t.Helper()

	stdout.Reset()
	clearCode := realMainWithLogger(ctx, []string{"--db", dbPath, "manager", "clear", "nsx-t-1"}, stdout, logger)
	if clearCode != 0 {
		t.Fatalf("manager clear exit code = %d, want 0", clearCode)
	}
	if !fileExists(t, dbPath) {
		t.Fatalf("manager database %q does not exist after --db clear", dbPath)
	}

	stdout.Reset()
	deleteCode := realMainWithLogger(ctx, []string{"--db", dbPath, "manager", "delete", "nsx-t-1"}, stdout, logger)
	if deleteCode != 0 {
		t.Fatalf("manager delete exit code = %d, want 0", deleteCode)
	}
	if fileExists(t, dbPath) {
		t.Fatalf("manager database %q still exists after --db delete", dbPath)
	}
}

func TestManagerCommandValidationRejectsInvalidUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := func() (*zap.Logger, error) {
		return zap.NewNop(), nil
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"manager", "bogus"}},
		{name: "missing manager name", args: []string{"manager", "add"}},
		{name: "unsafe manager name", args: []string{"manager", "add", "nsx/t-1"}},
		{
			name: "conflicting db and data dir",
			args: []string{
				"--db", filepath.Join(t.TempDir(), "single.db"),
				"--data-dir", t.TempDir(),
				"manager", "ls",
			},
		},
		{name: "unknown output", args: []string{"manager", "ls", "--output", "yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code := realMainWithLogger(ctx, tc.args, &bytes.Buffer{}, logger)
			if code == 0 {
				t.Fatalf("realMainWithLogger(%q) exit code = 0, want non-zero", tc.args)
			}
		})
	}
}

func TestRealMainReturnsFailureForInvalidUsage(t *testing.T) {
	if code := realMain(context.Background(), []string{"manager", "add"}); code != 1 {
		t.Fatalf("realMain() exit code = %d, want 1", code)
	}
}

func TestRealMainWithLoggerReturnsFailureWhenLoggerCannotBuild(t *testing.T) {
	t.Parallel()

	buildLogger := func() (*zap.Logger, error) {
		return nil, errLoggerFailed
	}
	code := realMainWithLogger(context.Background(), []string{"manager", "ls"}, &bytes.Buffer{}, buildLogger)
	if code != 1 {
		t.Fatalf("realMainWithLogger() exit code = %d, want 1", code)
	}
}

func TestRealMainWithLoggerReturnsFailureWhenFallbackStderrWriteFails(t *testing.T) {
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})

	closedStderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err = closedStderr.Close(); err != nil {
		t.Fatalf("closedStderr.Close() error = %v", err)
	}
	os.Stderr = closedStderr

	buildLogger := func() (*zap.Logger, error) {
		return nil, errLoggerFailed
	}
	code := realMainWithLogger(context.Background(), []string{"manager", "ls"}, &bytes.Buffer{}, buildLogger)
	if code != 1 {
		t.Fatalf("realMainWithLogger() exit code = %d, want 1", code)
	}
}

func TestParseManagerActionFlagsAcceptsEqualsOutputAndRejectsMissingOutputValue(t *testing.T) {
	t.Parallel()

	cfg := commandConfig{output: outputText}
	if err := parseManagerActionFlags([]string{"nsx-t-1", "--output=json"}, &cfg); err != nil {
		t.Fatalf("parseManagerActionFlags() error = %v", err)
	}
	if cfg.output != outputJSON {
		t.Fatalf("output = %q, want json", cfg.output)
	}

	cfg = commandConfig{output: outputText}
	if err := parseManagerActionFlags([]string{"nsx-t-1", "--output"}, &cfg); err == nil {
		t.Fatal("parseManagerActionFlags() error = nil, want error")
	}
}

func TestRenderManagerTextOutputAndWriterErrors(t *testing.T) {
	t.Parallel()

	manager := sqlite.ManagerInfo{Name: "nsx-t-1", Path: "/tmp/nsx-t-1.db"}
	stdout := &bytes.Buffer{}
	if err := renderManagerList(stdout, outputText, []sqlite.ManagerInfo{manager}); err != nil {
		t.Fatalf("renderManagerList() text error = %v", err)
	}
	if !strings.Contains(stdout.String(), "nsx-t-1\t/tmp/nsx-t-1.db") {
		t.Fatalf("renderManagerList() text stdout = %q, want manager row", stdout.String())
	}

	stdout.Reset()
	if err := renderManagerAction(stdout, outputText, "added", manager); err != nil {
		t.Fatalf("renderManagerAction() text error = %v", err)
	}
	if !strings.Contains(stdout.String(), "added\tnsx-t-1\t/tmp/nsx-t-1.db") {
		t.Fatalf("renderManagerAction() text stdout = %q, want action row", stdout.String())
	}

	failing := failingWriter{}
	if err := renderManagerList(failing, outputText, []sqlite.ManagerInfo{manager}); err == nil {
		t.Fatal("renderManagerList() text error = nil, want writer error")
	}
	if err := renderManagerList(failing, outputJSON, []sqlite.ManagerInfo{manager}); err == nil {
		t.Fatal("renderManagerList() JSON error = nil, want writer error")
	}
	if err := renderManagerAction(failing, outputText, "added", manager); err == nil {
		t.Fatal("renderManagerAction() text error = nil, want writer error")
	}
	if err := renderManagerAction(failing, outputJSON, "added", manager); err == nil {
		t.Fatal("renderManagerAction() JSON error = nil, want writer error")
	}
}

func TestNewLoggerAndStderrWriteSyncer(t *testing.T) {
	logger, err := newLogger()
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	if err = logger.Sync(); err != nil {
		t.Fatalf("logger.Sync() error = %v", err)
	}

	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer func() {
		if closeErr := stderrFile.Close(); closeErr != nil {
			t.Errorf("stderrFile.Close() error = %v", closeErr)
		}
	}()
	os.Stderr = stderrFile

	syncer := stderrWriteSyncer{}
	written, err := syncer.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len("hello") {
		t.Fatalf("Write() wrote %d bytes, want %d", written, len("hello"))
	}
	if err = syncer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestStderrWriteSyncerReportsWriteError(t *testing.T) {
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})

	closedStderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err = closedStderr.Close(); err != nil {
		t.Fatalf("closedStderr.Close() error = %v", err)
	}
	os.Stderr = closedStderr

	written, err := stderrWriteSyncer{}.Write([]byte("hello"))
	if err == nil {
		t.Fatal("Write() error = nil, want error")
	}
	if written != 0 {
		t.Fatalf("Write() wrote %d bytes, want 0", written)
	}
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errWriteFailed
}

func createSegmentResource(ctx context.Context, t *testing.T, dbPath string) {
	t.Helper()

	manager := openManagerForTest(ctx, t, "nsx-t-1", dbPath)
	defer closeManagerForTest(t, manager)
	_, err := sqlite.NewResourceStore(manager.DB, sqlite.ResourceStoreOptions{}).Mutate(ctx, sqlite.Mutation{
		Spec: sqlite.ResourceSpec{
			APIFamily:     sqlite.ResourceAPIFamilyPolicy,
			CollectionKey: "segments",
			Kind:          "segment",
			ResourceType:  "Segment",
			Path:          "/infra/segments/web",
			ParentPath:    "/infra",
			RelativePath:  "web",
		},
		Body:          json.RawMessage(`{"display_name":"web"}`),
		Username:      sqlite.DefaultAdminUsername,
		Operation:     sqlite.ResourceOperationCreate,
		RequestPath:   "/policy/api/v1/infra/segments/web",
		RouteTemplate: "/policy/api/v1/infra/segments/{segment-id}",
		StatusCode:    200,
	})
	if err != nil {
		t.Fatalf("Mutate() create segment error = %v", err)
	}
}

func openManagerForTest(ctx context.Context, t *testing.T, name string, dbPath string) sqlite.ManagerDatabase {
	t.Helper()

	manager, err := sqlite.OpenManagerDatabase(ctx, sqlite.OpenManagerDatabaseOptions{
		Name: name,
		Host: name,
		Path: dbPath,
	})
	if err != nil {
		t.Fatalf("OpenManagerDatabase() error = %v", err)
	}
	return manager
}

func closeManagerForTest(t *testing.T, manager sqlite.ManagerDatabase) {
	t.Helper()

	if err := manager.DB.Close(); err != nil {
		t.Fatalf("Close() manager database error = %v", err)
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	return !info.IsDir()
}

func requireUserInList(t *testing.T, got userListJSON, username string, role string) {
	t.Helper()

	for _, user := range got.Users {
		if user.Username == username {
			if user.Role != role {
				t.Fatalf("user %q role = %q, want %q", username, user.Role, role)
			}
			return
		}
	}
	t.Fatalf("user %q not found in %+v", username, got.Users)
}

func requireUserAbsent(t *testing.T, got userListJSON, username string) {
	t.Helper()

	for _, user := range got.Users {
		if user.Username == username {
			t.Fatalf("user %q found in %+v, want absent", username, got.Users)
		}
	}
}

func addUserForTest(
	ctx context.Context,
	t *testing.T,
	stdout *bytes.Buffer,
	logger func() (*zap.Logger, error),
	storageFlag string,
	storagePath string,
	username string,
	roleFlag string,
) {
	t.Helper()

	stdout.Reset()
	args := []string{storageFlag, storagePath, "user", "add", username, "--manager", "nsx-t-1", "--password", "secret"}
	if roleFlag != "" {
		args = append(args, roleFlag)
	}
	code := realMainWithLogger(ctx, args, stdout, logger)
	if code != 0 {
		t.Fatalf("user add %q exit code = %d, want 0", username, code)
	}
}

func requireCLIExitZero(
	ctx context.Context,
	t *testing.T,
	stdout *bytes.Buffer,
	logger func() (*zap.Logger, error),
	args ...string,
) {
	t.Helper()

	code := realMainWithLogger(ctx, args, stdout, logger)
	if code != 0 {
		t.Fatalf("realMainWithLogger(%q) exit code = %d, want 0", args, code)
	}
}
