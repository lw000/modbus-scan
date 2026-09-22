# Windows Service Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add native Windows service installation, lifecycle commands, SCM execution, and stable path handling to `modbus-scan.exe` without breaking console or CSV validation modes.

**Architecture:** Keep application startup in `cmd/modbus-scan`, isolate Windows SCM integration in a focused `internal/winservice` package with build-tagged implementations, and inject a small manager interface into command dispatch for unit testing. Resolve runtime file paths in `internal/appconfig` so console and SCM execution share identical semantics.

**Tech Stack:** Go 1.21, `golang.org/x/sys/windows/svc`, `golang.org/x/sys/windows/svc/mgr`, standard `flag`, `context`, and Go build constraints.

## Global Constraints

- Windows service name is exactly `modbus-scan`; display name is exactly `Modbus Scan`.
- Install with Windows automatic startup and the built-in `LocalSystem` account.
- Support exactly `install`, `uninstall`, `start`, `stop`, `restart`, and `status`.
- `install -config` stores absolute executable and configuration paths; its default is `configs/config.toml`.
- Do not add a new third-party module; promote the existing `golang.org/x/sys v0.20.0` requirement from indirect to direct.
- Preserve no-subcommand console execution and `-validate -csv` behavior.
- Resolve relative database and log paths against the configuration file directory.
- Never register or remove a real service from automated tests.
- Follow TDD: observe each new test fail for the intended reason before adding production code.

---

## File Structure

- `internal/appconfig/config.go`: load, validate, and resolve runtime paths relative to the config file.
- `internal/appconfig/config_test.go`: path-resolution regression tests.
- `internal/winservice/types.go`: shared service constants, public states, manager contract, and wait policy.
- `internal/winservice/manage_windows.go`: SCM-backed install/uninstall/start/stop/restart/status operations.
- `internal/winservice/manage_other.go`: explicit unsupported-platform manager.
- `internal/winservice/run_windows.go`: SCM session detection and service control handler.
- `internal/winservice/run_other.go`: console-only fallback for non-Windows builds.
- `internal/winservice/manage_windows_test.go`: state mapping and wait-loop tests without SCM mutation.
- `internal/winservice/run_windows_test.go`: service control-loop cancellation tests.
- `cmd/modbus-scan/main.go`: thin process entry, console signal context, and application composition.
- `cmd/modbus-scan/command.go`: top-level command parsing and manager dispatch.
- `cmd/modbus-scan/command_test.go`: command contract tests with a fake manager.
- `cmd/modbus-scan/main_test.go`: console/SCM routing and existing application regression tests.
- `configs/config.toml`: paths adjusted for config-directory-relative resolution.
- `README.md`: Windows service commands, permissions, and path semantics.
- `go.mod`, `go.sum`: direct `x/sys` dependency metadata.

---

### Task 1: Resolve Runtime Paths from the Configuration File

**Files:**
- Modify: `internal/appconfig/config.go`
- Modify: `internal/appconfig/config_test.go`
- Modify: `configs/config.toml`

**Interfaces:**
- Consumes: existing `func Load(path string) (Config, error)`.
- Produces: unchanged `Load` signature with `Database.Path` and non-empty `Log.File` returned as cleaned absolute paths.

- [ ] **Step 1: Add failing table-driven path tests**

Add tests that write a valid TOML file under `t.TempDir()/configs/config.toml`, call `Load`, and assert:

```go
func TestLoadResolvesRuntimePathsRelativeToConfig(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.toml")
	content := `[server]
host="127.0.0.1"
port=8080
[database]
path="../data/app.db"
[log]
console=false
file="../logs/app.log"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(configDir), "data", "app.db"); cfg.Database.Path != want {
		t.Fatalf("database path = %q, want %q", cfg.Database.Path, want)
	}
	if want := filepath.Join(filepath.Dir(configDir), "logs", "app.log"); cfg.Log.File != want {
		t.Fatalf("log path = %q, want %q", cfg.Log.File, want)
	}
}
```

Add a second case with absolute database/log paths and assert they remain unchanged after `filepath.Clean`.

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./internal/appconfig -run TestLoadResolvesRuntimePaths -count=1 -v`

Expected: FAIL because `Load` still returns relative paths.

- [ ] **Step 3: Implement path resolution after validation**

Add `path/filepath` and resolve from the absolute configuration directory:

```go
func Load(path string) (Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	cfg := Default()
	if _, err := toml.DecodeFile(absPath, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode service config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	base := filepath.Dir(absPath)
	cfg.Database.Path = resolvePath(base, cfg.Database.Path)
	if cfg.Log.File != "" {
		cfg.Log.File = resolvePath(base, cfg.Log.File)
	}
	return cfg, nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}
```

- [ ] **Step 4: Update example paths and run package tests**

Change `configs/config.toml` to:

```toml
[database]
path = "../data/modbus-scan.db"

[log]
file = "../logs/modbus-scan.log"
```

Run: `gofmt -w internal/appconfig/config.go internal/appconfig/config_test.go`

Run: `go test ./internal/appconfig ./cmd/modbus-scan -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the path semantics**

```powershell
git add internal/appconfig/config.go internal/appconfig/config_test.go configs/config.toml
git commit -m "fix: resolve runtime paths from config directory"
```

---

### Task 2: Add Testable Service Command Dispatch

**Files:**
- Create: `internal/winservice/types.go`
- Create: `cmd/modbus-scan/command.go`
- Create: `cmd/modbus-scan/command_test.go`
- Modify: `cmd/modbus-scan/main.go`

**Interfaces:**
- Produces `winservice.Manager` with `Install(executablePath, configPath string) error`, `Uninstall() error`, `Start() error`, `Stop(context.Context) error`, `Restart(context.Context) error`, and `Status() (winservice.State, error)`.
- Produces `func dispatchCommand(ctx context.Context, args []string, executablePath string, manager winservice.Manager, stdout io.Writer) (handled bool, err error)`.
- Produces `winservice.State` constants `StateStopped`, `StateStartPending`, `StateStopPending`, `StateRunning`, `StateContinuePending`, `StatePausePending`, `StatePaused`, and `StateUnknown`.

- [ ] **Step 1: Write failing dispatch tests using a recording manager**

Create a fake implementing the exact manager contract. Add separate tests for all six commands, unknown commands, rejected extra arguments, install default config, install explicit config, absolute path conversion, and status output. Representative assertions:

```go
func TestDispatchInstallUsesAbsolutePaths(t *testing.T) {
	mgr := &recordingManager{}
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte(validConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	handled, err := dispatchCommand(context.Background(), []string{"install", "-config", config}, `C:\apps\modbus-scan.exe`, mgr, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || mgr.operation != "install" {
		t.Fatalf("handled = %v, operation = %q", handled, mgr.operation)
	}
	if !filepath.IsAbs(mgr.configPath) || !filepath.IsAbs(mgr.executablePath) {
		t.Fatalf("paths are not absolute: exe=%q config=%q", mgr.executablePath, mgr.configPath)
	}
}

func TestDispatchStatusPrintsStableState(t *testing.T) {
	mgr := &recordingManager{state: winservice.StateRunning}
	var out bytes.Buffer
	handled, err := dispatchCommand(context.Background(), []string{"status"}, `C:\apps\modbus-scan.exe`, mgr, &out)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got, want := out.String(), "running\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
```

Assert `dispatchCommand(..., []string{"-config", path}, ...)` returns `handled == false`, preserving legacy flags.

- [ ] **Step 2: Run the new tests and verify RED**

Run: `go test ./cmd/modbus-scan -run TestDispatch -count=1 -v`

Expected: FAIL to compile because `dispatchCommand` and `winservice` types do not exist.

- [ ] **Step 3: Add shared types and minimal command parsing**

Define:

```go
package winservice

import "context"

const (
	Name        = "modbus-scan"
	DisplayName = "Modbus Scan"
)

type State string

const (
	StateStopped         State = "stopped"
	StateStartPending    State = "start_pending"
	StateStopPending     State = "stop_pending"
	StateRunning         State = "running"
	StateContinuePending State = "continue_pending"
	StatePausePending    State = "pause_pending"
	StatePaused          State = "paused"
	StateUnknown         State = "unknown"
)

type Manager interface {
	Install(executablePath, configPath string) error
	Uninstall() error
	Start() error
	Stop(context.Context) error
	Restart(context.Context) error
	Status() (State, error)
}
```

In `command.go`, treat only the exact six first arguments as service subcommands. Parse `install` with its own `flag.FlagSet`; reject positional leftovers for every command. Before `Install`, call `filepath.Abs` for both paths and `appconfig.Load(absConfig)` to validate it. Wrap errors as lowercase operation context, for example `install service: %w`.

- [ ] **Step 4: Finish command validation without changing process startup**

Keep `main()` and `run()` unchanged in this task. In `dispatchCommand`, reject unknown leading positional tokens as `unknown command`, reject leftover arguments after every known subcommand, and use a 30-second `context.WithTimeout` derived from the supplied context for stop/restart. This leaves a fully testable command layer while process wiring waits for the platform constructor in Task 3.

- [ ] **Step 5: Run tests and commit command dispatch**

Run: `gofmt -w cmd/modbus-scan/command.go cmd/modbus-scan/command_test.go cmd/modbus-scan/main.go internal/winservice/types.go`

Run: `go test ./cmd/modbus-scan -run 'TestDispatch|TestRun' -count=1`

Expected: PASS; these tests use the recording manager and do not access SCM.

```powershell
git add cmd/modbus-scan/command.go cmd/modbus-scan/command_test.go cmd/modbus-scan/main.go internal/winservice/types.go
git commit -m "feat: dispatch Windows service commands"
```

---

### Task 3: Implement Native Windows Service Management

**Files:**
- Create: `internal/winservice/manage_windows.go`
- Create: `internal/winservice/manage_other.go`
- Create: `internal/winservice/manage_windows_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `winservice.Manager`, state constants, `Name`, and `DisplayName` from Task 2.
- Produces `func NewManager() Manager` on all platforms.
- Windows implementation uses `mgr.Connect`, `mgr.CreateService`, `mgr.OpenService`, and bounded state polling.

- [ ] **Step 1: Write failing pure tests for state mapping and polling**

Keep the SCM boundary small by defining an unexported service handle interface:

```go
type serviceHandle interface {
	Query() (svc.Status, error)
	Start(args ...string) error
	Control(cmd svc.Cmd) (svc.Status, error)
	Delete() error
	Close() error
}
```

Test every `svc.State` mapping and a `waitForState(ctx, query, wanted)` helper with a scripted query function. Cover immediate success, transitional states, query error, and context timeout. Example:

```go
func TestWaitForStateStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForState(ctx, func() (svc.Status, error) {
		return svc.Status{State: svc.StopPending}, nil
	}, svc.Stopped)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
```

- [ ] **Step 2: Run Windows package tests and verify RED**

Run: `go test ./internal/winservice -run 'TestMapState|TestWaitForState' -count=1 -v`

Expected: FAIL because Windows management helpers do not exist.

- [ ] **Step 3: Implement SCM installation and lifecycle operations**

Use `mgr.Config{DisplayName: DisplayName, StartType: mgr.StartAutomatic}` and:

```go
service, err := manager.CreateService(
	Name,
	executablePath,
	mgr.Config{DisplayName: DisplayName, StartType: mgr.StartAutomatic},
	"-config", configPath,
)
```

Do not set a username or password, which preserves `LocalSystem`. Ensure every opened manager/service handle is closed and wrap close errors when no earlier error exists. Implement idempotent `Start` when already `svc.Running` and idempotent `Stop` when already `svc.Stopped`. `Restart` queries first, stops and waits when necessary, then starts and waits for `svc.Running`. Poll every 200 ms and honor the caller context.

`Uninstall` must query first and reject all states except `svc.Stopped`; then call `Delete`. Translate `windows.ERROR_ACCESS_DENIED` into an error that includes `please run in an administrator PowerShell` while preserving the original error with `%w`.

- [ ] **Step 4: Add the non-Windows implementation and direct dependency**

Under `//go:build !windows`, return an `unsupportedManager` whose six methods return:

```go
var errUnsupported = errors.New("Windows service management is only supported on Windows")
```

Run: `go mod edit -require=golang.org/x/sys@v0.20.0`

Run: `go mod tidy`

Verify `go.mod` lists `golang.org/x/sys v0.20.0` in the direct requirement block.

- [ ] **Step 5: Verify tests, cross-build, and commit**

Run: `gofmt -w internal/winservice/manage_windows.go internal/winservice/manage_other.go internal/winservice/manage_windows_test.go`

Run: `go test ./internal/winservice ./cmd/modbus-scan -count=1`

Run: `$env:GOOS='linux'; go test ./internal/winservice ./cmd/modbus-scan -count=1; Remove-Item Env:GOOS`

Expected: both Windows and Linux-target package tests compile and pass without accessing SCM.

```powershell
git add internal/winservice go.mod go.sum cmd/modbus-scan
git commit -m "feat: manage native Windows service"
```

---

### Task 4: Run the Application Under the Windows SCM

**Files:**
- Create: `internal/winservice/run_windows.go`
- Create: `internal/winservice/run_other.go`
- Create: `internal/winservice/run_windows_test.go`
- Modify: `cmd/modbus-scan/main.go`
- Modify: `cmd/modbus-scan/main_test.go`

**Interfaces:**
- Produces `func IsService() (bool, error)` on all platforms.
- Produces `func Run(run func(context.Context) error) error` on all platforms; on Windows this enters `svc.Run(Name, handler)`.
- SCM handler accepts `svc.Stop` and `svc.Shutdown`, rejects unsupported controls, and cancels the application context.

- [ ] **Step 1: Write a failing service handler test**

Extract the control loop behind channels so it can be tested without SCM registration. Feed an interrogate request and assert the current status is echoed; then feed `svc.Stop` and assert the application context is canceled and statuses include `StartPending`, `Running`, `StopPending`, and `Stopped` in order.

```go
func TestExecuteCancelsApplicationOnStop(t *testing.T) {
	requests := make(chan svc.ChangeRequest, 1)
	statuses := make(chan svc.Status, 8)
	started := make(chan struct{})
	handler := serviceHandler{run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}}
	done := make(chan error, 1)
	go func() {
		_, _, err := handler.Execute(nil, requests, statuses)
		done <- err
	}()
	<-started
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertStateSequence(t, statuses, svc.StartPending, svc.Running, svc.StopPending, svc.Stopped)
}
```

Add the equivalent `svc.Shutdown` case and a run-error case.

- [ ] **Step 2: Run the handler tests and verify RED**

Run: `go test ./internal/winservice -run TestExecute -count=1 -v`

Expected: FAIL because `serviceHandler` and runtime functions do not exist.

- [ ] **Step 3: Implement SCM execution**

In `run_windows.go`:

```go
func IsService() (bool, error) {
	interactive, err := svc.IsAnInteractiveSession()
	if err != nil {
		return false, fmt.Errorf("detect Windows service session: %w", err)
	}
	return !interactive, nil
}

func Run(run func(context.Context) error) error {
	if err := svc.Run(Name, serviceHandler{run: run}); err != nil {
		return fmt.Errorf("run Windows service: %w", err)
	}
	return nil
}
```

The handler creates `context.WithCancel`, starts `run(ctx)` in a buffered error channel, reports accepted controls `svc.AcceptStop | svc.AcceptShutdown`, and returns only after the application exits. During stop, report `StopPending` with a nonzero `WaitHint` and incrementing `CheckPoint` on a ticker until exit. Return a service-specific nonzero exit code when `run` fails and preserve the Go error for process logging.

The non-Windows file returns `false, nil` from `IsService` and an unsupported error from `Run`.

- [ ] **Step 4: Route service and console execution in main**

Refactor to a testable function:

```go
func execute(args []string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	manager := winservice.NewManager()
	handled, err := dispatchCommand(ctx, args, executablePath, manager, stdout)
	if handled || err != nil {
		return err
	}
	serviceMode, err := winservice.IsService()
	if err != nil {
		return err
	}
	if serviceMode {
		return winservice.Run(func(serviceCtx context.Context) error {
			return run(serviceCtx, args)
		})
	}
	return run(ctx, args)
}
```

Keep `main()` limited to calling `execute(os.Args[1:], os.Stdout, os.Stderr)`, printing `modbus-scan: %v`, and exiting nonzero. Ensure `-validate` remains console-only by checking it before SCM detection, or by relying on installed SCM arguments containing only `-config` while documenting and testing the chosen routing.

- [ ] **Step 5: Verify runtime routing and commit**

Run: `gofmt -w internal/winservice/run_windows.go internal/winservice/run_other.go internal/winservice/run_windows_test.go cmd/modbus-scan/main.go cmd/modbus-scan/main_test.go`

Run: `go test ./internal/winservice ./cmd/modbus-scan -count=1`

Expected: PASS, including existing cancellation and CSV validation tests.

```powershell
git add internal/winservice cmd/modbus-scan/main.go cmd/modbus-scan/main_test.go
git commit -m "feat: run collector under Windows SCM"
```

---

### Task 5: Document and Fully Verify Windows Service Support

**Files:**
- Modify: `README.md`
- Modify: `build.bat` only if verification exposes a Windows build regression.

**Interfaces:**
- Consumes: final command contract and path rules from Tasks 1–4.
- Produces: copy-pasteable administrator PowerShell operating instructions.

- [ ] **Step 1: Add README command documentation**

Document all of the following explicitly:

```powershell
# Run these commands from an Administrator PowerShell.
.\modbus-scan.exe install -config .\configs\config.toml
.\modbus-scan.exe start
.\modbus-scan.exe status
.\modbus-scan.exe restart
.\modbus-scan.exe stop
.\modbus-scan.exe uninstall
```

State that installation uses service name `modbus-scan`, display name `Modbus Scan`, `LocalSystem`, and automatic startup. Explain that install captures absolute executable/config paths, relative database/log paths are based on the config directory, moving the executable or config requires uninstall/reinstall, and uninstall requires the service to be stopped.

- [ ] **Step 2: Run formatting and focused tests**

Run: `gofmt -w cmd/modbus-scan internal/appconfig internal/winservice`

Run: `go test ./internal/appconfig ./internal/winservice ./cmd/modbus-scan -count=1`

Expected: PASS with zero failures.

- [ ] **Step 3: Run full static and test verification**

Run: `go test ./... -count=1`

Expected: PASS for every package.

Run: `go vet ./...`

Expected: exit code 0 with no diagnostics.

- [ ] **Step 4: Build the Windows executable outside tracked output paths**

Run: `go build -o C:\tmp\modbus-scan-service.exe ./cmd/modbus-scan`

Expected: exit code 0 and `C:\tmp\modbus-scan-service.exe` exists.

Run: `git status --short`

Expected: only intended source, test, configuration, module, and documentation changes; no generated binary or cache is added.

- [ ] **Step 5: Perform administrator-only manual acceptance**

This step is intentionally not part of automated tests. From an Administrator PowerShell, use a disposable deployment directory and run the six documented commands. Verify with `Get-Service modbus-scan` that startup type is Automatic and state transitions match each command. Start the service and confirm the database/log files appear at the config-relative resolved paths. Stop and uninstall it before deleting the disposable directory.

If the current session is not elevated, record this step as unverified rather than attempting privilege escalation or claiming it passed.

- [ ] **Step 6: Commit documentation and final adjustments**

```powershell
git add README.md build.bat
git commit -m "docs: explain Windows service operations"
```

If `build.bat` was not changed, omit it from `git add`.

---

## Final Review Checklist

- [ ] Every production behavior was preceded by a test that failed for the expected reason.
- [ ] No automated test creates, starts, stops, or deletes a real Windows service.
- [ ] All six commands have success, invalid-argument, and wrapped-error coverage.
- [ ] SCM stop and shutdown both cancel the existing application context.
- [ ] Service handles and manager handles close on every path.
- [ ] README commands match the implemented parser exactly.
- [ ] `go test ./... -count=1`, `go vet ./...`, and the Windows build have fresh successful output.
- [ ] Any skipped administrator acceptance test is explicitly reported as unverified.
