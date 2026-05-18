// Command mocknsx manages local NSX-T mock API state without starting the HTTP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"nsx-t-mockapi/internal/sqlite"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	outputText = "text"
	outputJSON = "json"
)

var errUsage = errors.New("usage: mocknsx [--data-dir DIR] <manager|user> <command> [args] [--output text|json]")

const minCommandParts = 2

type commandConfig struct {
	dataDir          string
	managerDir       string
	dbPath           string
	output           string
	group            commandGroup
	manager          managerCommand
	user             userCommand
	name             string
	managerName      string
	password         string
	passwordProvided bool
	role             sqlite.Role
}

type commandGroup string

const (
	commandGroupManager commandGroup = "manager"
	commandGroupUser    commandGroup = "user"
)

type managerCommand string

const (
	managerCommandList   managerCommand = "ls"
	managerCommandAdd    managerCommand = "add"
	managerCommandClear  managerCommand = "clear"
	managerCommandDelete managerCommand = "delete"
)

type userCommand string

const (
	userCommandList   userCommand = "ls"
	userCommandAdd    userCommand = "add"
	userCommandDelete userCommand = "delete"
)

type managerListOutput struct {
	Managers []managerOutput `json:"managers"`
	Count    int             `json:"count"`
}

type managerActionOutput struct {
	Manager managerOutput `json:"manager"`
	Action  string        `json:"action"`
}

type managerOutput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type userListOutput struct {
	Users []userOutput `json:"users"`
	Count int          `json:"count"`
}

type userActionOutput struct {
	User   userOutput `json:"user"`
	Action string     `json:"action"`
}

type userOutput struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type parsedUserRoles struct {
	readOnly  bool
	readWrite bool
	admin     bool
}

func main() {
	os.Exit(realMain(context.Background(), os.Args[1:]))
}

func realMain(ctx context.Context, args []string) int {
	return realMainWithLogger(ctx, args, os.Stdout, newLogger)
}

func realMainWithLogger(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	buildLogger func() (*zap.Logger, error),
) int {
	logger, err := buildLogger()
	if err != nil {
		if writeErr := writeFallbackLog("fatal", "create logger", err); writeErr != nil {
			return 1
		}
		return 1
	}

	exitCode := 0
	if err = run(ctx, args, stdout, logger); err != nil {
		logger.Error("command failed", zap.Error(err))
		exitCode = 1
	}

	if syncErr := logger.Sync(); syncErr != nil {
		if writeErr := writeFallbackLog("warn", "sync logger", syncErr); writeErr != nil {
			return 1
		}
	}

	return exitCode
}

func run(ctx context.Context, args []string, stdout io.Writer, logger *zap.Logger) error {
	cfg, err := parseCommand(args)
	if err != nil {
		return err
	}

	catalog, err := sqlite.NewManagerCatalog(sqlite.ManagerCatalogOptions{
		DataDir:    cfg.dataDir,
		ManagerDir: cfg.managerDir,
		DBPath:     cfg.dbPath,
		Logger:     logger,
	})
	if err != nil {
		return fmt.Errorf("create manager catalog: %w", err)
	}

	logger.Info(
		"starting cli command",
		zap.String("group", string(cfg.group)),
		zap.String("command", cfg.commandName()),
		zap.String("manager_name", cfg.name),
		zap.String("selected_manager", cfg.managerName),
		zap.String("username", cfg.userLogName()),
		zap.String("role", string(cfg.role)),
		zap.Bool("password_provided", cfg.passwordProvided),
		zap.String("data_dir", cfg.dataDir),
		zap.String("manager_dir", cfg.managerDir),
		zap.String("db_path", cfg.dbPath),
		zap.String("output", cfg.output),
	)
	result, err := executeCommand(ctx, catalog, cfg)
	if err != nil {
		return err
	}
	if err = renderCommandResult(stdout, cfg, result); err != nil {
		return err
	}
	logger.Info(
		"completed cli command",
		zap.String("group", string(cfg.group)),
		zap.String("command", cfg.commandName()),
		zap.String("manager_name", cfg.name),
		zap.String("selected_manager", cfg.managerName),
		zap.String("username", cfg.userLogName()),
		zap.String("role", string(cfg.role)),
		zap.String("data_dir", cfg.dataDir),
		zap.String("manager_dir", cfg.managerDir),
		zap.String("db_path", cfg.dbPath),
	)
	return nil
}

type commandResult struct {
	managers []sqlite.ManagerInfo
	manager  sqlite.ManagerInfo
	users    []sqlite.UserSummary
	user     sqlite.UserSummary
	action   string
}

func executeCommand(ctx context.Context, catalog sqlite.ManagerCatalog, cfg commandConfig) (commandResult, error) {
	switch cfg.group {
	case commandGroupManager:
		return executeManagerCommand(ctx, catalog, cfg)
	case commandGroupUser:
		return executeUserCommand(ctx, catalog, cfg)
	default:
		return commandResult{}, fmt.Errorf("%w: unsupported command group %q", errUsage, cfg.group)
	}
}

func executeManagerCommand(
	ctx context.Context,
	catalog sqlite.ManagerCatalog,
	cfg commandConfig,
) (commandResult, error) {
	switch cfg.manager {
	case managerCommandList:
		managers, listErr := catalog.List(ctx)
		if listErr != nil {
			return commandResult{}, fmt.Errorf("list manager databases: %w", listErr)
		}
		return commandResult{managers: managers}, nil
	case managerCommandAdd:
		manager, addErr := catalog.Add(ctx, cfg.name)
		if addErr != nil {
			return commandResult{}, fmt.Errorf("add manager database %q: %w", cfg.name, addErr)
		}
		return commandResult{manager: manager, action: "added"}, nil
	case managerCommandClear:
		manager, clearErr := catalog.Clear(ctx, cfg.name)
		if clearErr != nil {
			return commandResult{}, fmt.Errorf("clear manager database %q: %w", cfg.name, clearErr)
		}
		return commandResult{manager: manager, action: "cleared"}, nil
	case managerCommandDelete:
		manager, deleteErr := catalog.Delete(ctx, cfg.name)
		if deleteErr != nil {
			return commandResult{}, fmt.Errorf("delete manager database %q: %w", cfg.name, deleteErr)
		}
		return commandResult{manager: manager, action: "deleted"}, nil
	default:
		return commandResult{}, fmt.Errorf("%w: unsupported manager command %q", errUsage, cfg.manager)
	}
}

func executeUserCommand(
	ctx context.Context,
	catalog sqlite.ManagerCatalog,
	cfg commandConfig,
) (result commandResult, retErr error) {
	manager, err := catalog.OpenSelected(ctx, cfg.managerName)
	if err != nil {
		return commandResult{}, fmt.Errorf("open selected manager database %q: %w", cfg.managerName, err)
	}
	defer func() {
		closeErr := manager.DB.Close()
		if closeErr == nil {
			return
		}
		if retErr == nil {
			retErr = fmt.Errorf("close selected manager database %q: %w", manager.Path, closeErr)
			return
		}
		retErr = fmt.Errorf("%w; close selected manager database %q: %w", retErr, manager.Path, closeErr)
	}()

	store := sqlite.NewUserStore(manager.DB)
	switch cfg.user {
	case userCommandList:
		return executeUserList(ctx, store, manager.Name)
	case userCommandAdd:
		return executeUserAdd(ctx, store, manager.Name, cfg)
	case userCommandDelete:
		return executeUserDelete(ctx, store, manager.Name, cfg.name)
	default:
		return commandResult{}, fmt.Errorf("%w: unsupported user command %q", errUsage, cfg.user)
	}
}

func executeUserList(ctx context.Context, store sqlite.UserStore, managerName string) (commandResult, error) {
	users, err := store.ListUsers(ctx)
	if err != nil {
		return commandResult{}, fmt.Errorf("list users for manager %q: %w", managerName, err)
	}
	return commandResult{users: users}, nil
}

func executeUserAdd(
	ctx context.Context,
	store sqlite.UserStore,
	managerName string,
	cfg commandConfig,
) (commandResult, error) {
	password := cfg.password
	if !cfg.passwordProvided {
		password = cfg.name
	}
	user, err := store.AddUser(ctx, sqlite.User{
		Username: cfg.name,
		Password: password,
		Role:     cfg.role,
	})
	if err != nil {
		return commandResult{}, fmt.Errorf("add user %q for manager %q: %w", cfg.name, managerName, err)
	}
	return commandResult{user: user, action: "added"}, nil
}

func executeUserDelete(
	ctx context.Context,
	store sqlite.UserStore,
	managerName string,
	username string,
) (commandResult, error) {
	user, err := store.DeleteUser(ctx, username)
	if err != nil {
		return commandResult{}, fmt.Errorf("delete user %q for manager %q: %w", username, managerName, err)
	}
	return commandResult{user: user, action: "deleted"}, nil
}

func parseCommand(args []string) (commandConfig, error) {
	cfg, rest, err := parseGlobalFlags(args)
	if err != nil {
		return commandConfig{}, err
	}
	if len(rest) < minCommandParts {
		return commandConfig{}, errUsage
	}
	if cfg.group, err = parseCommandGroup(rest[0]); err != nil {
		return commandConfig{}, err
	}

	if err = parseSubcommand(rest[1:], &cfg); err != nil {
		return commandConfig{}, err
	}
	if cfg.dataDir == "" && cfg.managerDir == "" && cfg.dbPath == "" {
		return commandConfig{}, fmt.Errorf("%w: storage path is required", errUsage)
	}
	if err = validateOutput(cfg.output); err != nil {
		return commandConfig{}, err
	}
	return cfg, nil
}

func parseCommandGroup(value string) (commandGroup, error) {
	switch commandGroup(value) {
	case commandGroupManager:
		return commandGroupManager, nil
	case commandGroupUser:
		return commandGroupUser, nil
	default:
		return "", fmt.Errorf("%w: unknown command %q", errUsage, value)
	}
}

func parseSubcommand(args []string, cfg *commandConfig) error {
	switch cfg.group {
	case commandGroupManager:
		return parseManagerCommand(args, cfg)
	case commandGroupUser:
		return parseUserCommand(args, cfg)
	default:
		return fmt.Errorf("%w: unsupported command group %q", errUsage, cfg.group)
	}
}

func parseGlobalFlags(args []string) (commandConfig, []string, error) {
	flags := flag.NewFlagSet("mocknsx", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", "./data", "HTTP-compatible data directory")
	managerDir := flags.String("manager-dir", "", "directory containing manager database directories")
	dbPath := flags.String("db", "", "single manager database file")
	if err := flags.Parse(args); err != nil {
		return commandConfig{}, nil, fmt.Errorf("%w: %w", errUsage, err)
	}
	visitedStorageFlags := map[string]bool{}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "data-dir", "manager-dir", "db":
			visitedStorageFlags[f.Name] = true
		}
	})
	if len(visitedStorageFlags) > 1 {
		return commandConfig{}, nil, fmt.Errorf("%w: storage flags are mutually exclusive", errUsage)
	}
	cfg := commandConfig{dataDir: *dataDir, managerDir: *managerDir, dbPath: *dbPath, output: outputText}
	if visitedStorageFlags["manager-dir"] || visitedStorageFlags["db"] {
		cfg.dataDir = ""
	}
	return cfg, flags.Args(), nil
}

func parseManagerCommand(args []string, cfg *commandConfig) error {
	switch managerCommand(args[0]) {
	case managerCommandList:
		cfg.manager = managerCommandList
		return parseManagerListFlags(args[1:], cfg)
	case managerCommandAdd, managerCommandClear, managerCommandDelete:
		cfg.manager = managerCommand(args[0])
		return parseManagerActionFlags(args[1:], cfg)
	default:
		return fmt.Errorf("%w: unknown manager command %q", errUsage, args[0])
	}
}

func parseUserCommand(args []string, cfg *commandConfig) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: user command is required", errUsage)
	}
	cfg.role = sqlite.RoleReadWrite
	switch userCommand(args[0]) {
	case userCommandList:
		cfg.user = userCommandList
		return parseUserListFlags(args[1:], cfg)
	case userCommandAdd:
		cfg.user = userCommandAdd
		return parseUserAddFlags(args[1:], cfg)
	case userCommandDelete:
		cfg.user = userCommandDelete
		return parseUserDeleteFlags(args[1:], cfg)
	default:
		return fmt.Errorf("%w: unknown user command %q", errUsage, args[0])
	}
}

func parseUserListFlags(args []string, cfg *commandConfig) error {
	flags := flag.NewFlagSet("user ls", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", cfg.output, "output format: text or json")
	managerName := flags.String("manager", "", "manager database name")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected arguments %q", errUsage, flags.Args())
	}
	cfg.output = *output
	cfg.managerName = *managerName
	return validateSelectedManager(cfg)
}

func parseUserAddFlags(args []string, cfg *commandConfig) error {
	positional, err := parseUserMixedFlags(args, cfg, true)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("%w: user add requires exactly one username", errUsage)
	}
	cfg.name = positional[0]
	if validateErr := sqlite.ValidateUsername(cfg.name); validateErr != nil {
		return fmt.Errorf("%w: %w", errUsage, validateErr)
	}
	return validateSelectedManager(cfg)
}

func parseUserDeleteFlags(args []string, cfg *commandConfig) error {
	positional, err := parseUserMixedFlags(args, cfg, false)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("%w: user delete requires exactly one username", errUsage)
	}
	cfg.name = positional[0]
	if validateErr := sqlite.ValidateUsername(cfg.name); validateErr != nil {
		return fmt.Errorf("%w: %w", errUsage, validateErr)
	}
	return validateSelectedManager(cfg)
}

func parseUserMixedFlags(args []string, cfg *commandConfig, allowAddFlags bool) ([]string, error) {
	positional := make([]string, 0, 1)
	roles := parsedUserRoles{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)
			continue
		}
		next, err := parseUserMixedFlag(args, i, cfg, &roles, allowAddFlags)
		if err != nil {
			return nil, err
		}
		i = next
	}
	if err := applyUserRoleFlags(cfg, roles.readOnly, roles.readWrite, roles.admin); err != nil {
		return nil, err
	}
	return positional, nil
}

func parseUserMixedFlag(
	args []string,
	index int,
	cfg *commandConfig,
	roles *parsedUserRoles,
	allowAddFlags bool,
) (int, error) {
	next, handled, err := parseUserValueFlag(args, index, cfg, allowAddFlags)
	if handled || err != nil {
		return next, err
	}
	if parseUserRoleFlag(args[index], roles, allowAddFlags) {
		return index, nil
	}
	return index, fmt.Errorf("%w: unknown user flag %q", errUsage, args[index])
}

func parseUserValueFlag(
	args []string,
	index int,
	cfg *commandConfig,
	allowPassword bool,
) (int, bool, error) {
	arg := args[index]
	switch {
	case arg == "--output":
		value, next, err := requireFlagValue(args, index, "output")
		cfg.output = value
		return next, true, err
	case strings.HasPrefix(arg, "--output="):
		cfg.output = strings.TrimPrefix(arg, "--output=")
		return index, true, nil
	case arg == "--manager":
		value, next, err := requireFlagValue(args, index, "manager")
		cfg.managerName = value
		return next, true, err
	case strings.HasPrefix(arg, "--manager="):
		cfg.managerName = strings.TrimPrefix(arg, "--manager=")
		return index, true, nil
	case allowPassword && arg == "--password":
		value, next, err := requireFlagValue(args, index, "password")
		cfg.password = value
		cfg.passwordProvided = true
		return next, true, err
	case allowPassword && strings.HasPrefix(arg, "--password="):
		cfg.password = strings.TrimPrefix(arg, "--password=")
		cfg.passwordProvided = true
		return index, true, nil
	default:
		return index, false, nil
	}
}

func parseUserRoleFlag(arg string, roles *parsedUserRoles, allowRole bool) bool {
	if !allowRole {
		return false
	}
	switch arg {
	case "--read-only":
		roles.readOnly = true
		return true
	case "--read-write":
		roles.readWrite = true
		return true
	case "--admin":
		roles.admin = true
		return true
	default:
		return false
	}
}

func requireFlagValue(args []string, index int, name string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("%w: --%s requires a value", errUsage, name)
	}
	value := args[next]
	if strings.HasPrefix(value, "--") {
		return "", index, fmt.Errorf("%w: --%s requires a value", errUsage, name)
	}
	return value, next, nil
}

func parseManagerListFlags(args []string, cfg *commandConfig) error {
	flags := flag.NewFlagSet("manager ls", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", cfg.output, "output format: text or json")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected arguments %q", errUsage, flags.Args())
	}
	cfg.output = *output
	return nil
}

func parseManagerActionFlags(args []string, cfg *commandConfig) error {
	expectOutputValue := false
	for _, arg := range args {
		if expectOutputValue {
			cfg.output = arg
			expectOutputValue = false
			continue
		}
		switch arg {
		case "--output":
			expectOutputValue = true
		default:
			if value, ok := strings.CutPrefix(arg, "--output="); ok {
				cfg.output = value
				continue
			}
			if cfg.name != "" {
				return fmt.Errorf("%w: unexpected argument %q", errUsage, arg)
			}
			cfg.name = arg
		}
	}
	if expectOutputValue {
		return fmt.Errorf("%w: --output requires a value", errUsage)
	}
	if cfg.name == "" {
		return fmt.Errorf("%w: manager name is required", errUsage)
	}
	return nil
}

func renderCommandResult(stdout io.Writer, cfg commandConfig, result commandResult) error {
	if cfg.group == commandGroupUser {
		return renderUserCommandResult(stdout, cfg.output, result)
	}
	if result.action == "" {
		return renderManagerList(stdout, cfg.output, result.managers)
	}
	return renderManagerAction(stdout, cfg.output, result.action, result.manager)
}

func validateOutput(output string) error {
	switch output {
	case outputText, outputJSON:
		return nil
	default:
		return fmt.Errorf("%w: unknown output %q", errUsage, output)
	}
}

func renderUserCommandResult(stdout io.Writer, output string, result commandResult) error {
	if result.action == "" {
		return renderUserList(stdout, output, result.users)
	}
	return renderUserAction(stdout, output, result.action, result.user)
}

func renderManagerList(stdout io.Writer, output string, managers []sqlite.ManagerInfo) error {
	if output == outputJSON {
		rows := make([]managerOutput, 0, len(managers))
		for _, manager := range managers {
			rows = append(rows, managerOutput{Name: manager.Name, Path: manager.Path})
		}
		if err := json.NewEncoder(stdout).Encode(managerListOutput{Managers: rows, Count: len(rows)}); err != nil {
			return fmt.Errorf("write manager list JSON: %w", err)
		}
		return nil
	}
	for _, manager := range managers {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", manager.Name, manager.Path); err != nil {
			return fmt.Errorf("write manager list text: %w", err)
		}
	}
	return nil
}

func renderManagerAction(stdout io.Writer, output string, action string, manager sqlite.ManagerInfo) error {
	row := managerOutput{Name: manager.Name, Path: manager.Path}
	if output == outputJSON {
		if err := json.NewEncoder(stdout).Encode(managerActionOutput{Manager: row, Action: action}); err != nil {
			return fmt.Errorf("write manager action JSON: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", action, row.Name, row.Path); err != nil {
		return fmt.Errorf("write manager action text: %w", err)
	}
	return nil
}

func renderUserList(stdout io.Writer, output string, users []sqlite.UserSummary) error {
	if output == outputJSON {
		rows := make([]userOutput, 0, len(users))
		for _, user := range users {
			rows = append(rows, userOutput{Username: user.Username, Role: string(user.Role)})
		}
		if err := json.NewEncoder(stdout).Encode(userListOutput{Users: rows, Count: len(rows)}); err != nil {
			return fmt.Errorf("write user list JSON: %w", err)
		}
		return nil
	}
	for _, user := range users {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", user.Username, user.Role); err != nil {
			return fmt.Errorf("write user list text: %w", err)
		}
	}
	return nil
}

func renderUserAction(stdout io.Writer, output string, action string, user sqlite.UserSummary) error {
	row := userOutput{Username: user.Username, Role: string(user.Role)}
	if output == outputJSON {
		if err := json.NewEncoder(stdout).Encode(userActionOutput{User: row, Action: action}); err != nil {
			return fmt.Errorf("write user action JSON: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", action, row.Username, row.Role); err != nil {
		return fmt.Errorf("write user action text: %w", err)
	}
	return nil
}

func applyUserRoleFlags(cfg *commandConfig, readOnly bool, readWrite bool, admin bool) error {
	selected := 0
	if readOnly {
		selected++
		cfg.role = sqlite.RoleReadOnly
	}
	if readWrite {
		selected++
		cfg.role = sqlite.RoleReadWrite
	}
	if admin {
		selected++
		cfg.role = sqlite.RoleAdmin
	}
	if selected > 1 {
		return fmt.Errorf("%w: role flags are mutually exclusive", errUsage)
	}
	return nil
}

func validateSelectedManager(cfg *commandConfig) error {
	if cfg.dbPath == "" && cfg.managerName == "" {
		return fmt.Errorf("%w: --manager is required for directory storage modes", errUsage)
	}
	if cfg.managerName == "" {
		return nil
	}
	_, err := sqlite.NormalizeManagerHost(cfg.managerName)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	return nil
}

func (cfg commandConfig) commandName() string {
	switch cfg.group {
	case commandGroupManager:
		return string(cfg.manager)
	case commandGroupUser:
		return string(cfg.user)
	default:
		return ""
	}
}

func (cfg commandConfig) userLogName() string {
	if cfg.group != commandGroupUser {
		return ""
	}
	return cfg.name
}

func newLogger() (*zap.Logger, error) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		stderrWriteSyncer{},
		zap.DebugLevel,
	)
	return zap.New(core, zap.AddCaller()), nil
}

type stderrWriteSyncer struct{}

func (stderrWriteSyncer) Write(p []byte) (int, error) {
	written, err := os.Stderr.Write(p)
	if err != nil {
		return written, fmt.Errorf("write stderr: %w", err)
	}
	return written, nil
}

func (stderrWriteSyncer) Sync() error {
	return nil
}

func writeFallbackLog(level string, message string, err error) error {
	_, writeErr := fmt.Fprintf(
		os.Stderr,
		"{\"level\":%q,\"msg\":%q,\"error\":%q}\n",
		level,
		message,
		err.Error(),
	)
	if writeErr != nil {
		return fmt.Errorf("write fallback log: %w", writeErr)
	}
	return nil
}
