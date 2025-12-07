package pawgui

import (
	"os"
	"path/filepath"

	"github.com/phroun/pawscript"
)

// CreateFileAccessConfig creates a FileAccessConfig for script execution.
// This allows read access to the script directory and current working directory,
// and write access to specific subdirectories (saves, output).
func CreateFileAccessConfig(scriptDir string) *pawscript.FileAccessConfig {
	cwd, _ := os.Getwd()
	tmpDir := os.TempDir()

	return &pawscript.FileAccessConfig{
		ReadRoots: []string{scriptDir, cwd, tmpDir},
		WriteRoots: []string{
			filepath.Join(scriptDir, "saves"),
			filepath.Join(scriptDir, "output"),
			filepath.Join(cwd, "saves"),
			filepath.Join(cwd, "output"),
			tmpDir,
		},
		ExecRoots: []string{
			filepath.Join(scriptDir, "helpers"),
			filepath.Join(scriptDir, "bin"),
		},
	}
}

// ScriptRunner handles script execution with REPL integration.
type ScriptRunner struct {
	channels     *ConsoleChannels
	configHelper *ConfigHelper
	repl         *pawscript.REPL
	outputFunc   func(string)

	// Callbacks
	OnScriptStart func()
	OnScriptEnd   func()
}

// ScriptRunnerOptions configures the ScriptRunner.
type ScriptRunnerOptions struct {
	Channels     *ConsoleChannels
	ConfigHelper *ConfigHelper
	OutputFunc   func(string) // Function to output text to terminal
}

// NewScriptRunner creates a new ScriptRunner.
func NewScriptRunner(opts ScriptRunnerOptions) *ScriptRunner {
	return &ScriptRunner{
		channels:     opts.Channels,
		configHelper: opts.ConfigHelper,
		outputFunc:   opts.OutputFunc,
	}
}

// CreateREPL creates a new REPL instance.
func (sr *ScriptRunner) CreateREPL(showBanner bool) *pawscript.REPL {
	optLevel := 1
	if sr.configHelper != nil {
		optLevel = sr.configHelper.GetOptimizationLevel()
	}

	sr.repl = pawscript.NewREPL(pawscript.REPLConfig{
		Debug:        false,
		Unrestricted: false,
		OptLevel:     optLevel,
		ShowBanner:   showBanner,
		IOConfig:     sr.channels.GetIOConfig(),
	}, sr.outputFunc)

	return sr.repl
}

// StartREPL starts the REPL.
func (sr *ScriptRunner) StartREPL() {
	if sr.repl != nil {
		sr.repl.Start()
	}
}

// StopREPL stops the current REPL.
func (sr *ScriptRunner) StopREPL() {
	if sr.repl != nil {
		sr.repl.Stop()
	}
}

// GetREPL returns the current REPL instance.
func (sr *ScriptRunner) GetREPL() *pawscript.REPL {
	return sr.repl
}

// IsREPLRunning returns true if the REPL is running.
func (sr *ScriptRunner) IsREPLRunning() bool {
	return sr.repl != nil && sr.repl.IsRunning()
}

// HandleInput sends input to the REPL.
func (sr *ScriptRunner) HandleInput(data []byte) {
	if sr.repl != nil && sr.repl.IsRunning() {
		sr.repl.HandleInput(data)
	}
}

// ExecuteScript executes a script file.
// The script runs in a goroutine. When complete, the REPL is restarted.
// Returns immediately after starting the script.
func (sr *ScriptRunner) ExecuteScript(filePath string, content []byte, onComplete func()) {
	// Stop current REPL
	sr.StopREPL()

	// Clear any pending input
	sr.channels.ClearInput()

	// Notify script start
	if sr.OnScriptStart != nil {
		sr.OnScriptStart()
	}

	// Get script directory for file access
	scriptDir := filepath.Dir(filePath)
	fileAccess := CreateFileAccessConfig(scriptDir)

	// Get optimization level
	optLevel := 1
	if sr.configHelper != nil {
		optLevel = sr.configHelper.GetOptimizationLevel()
	}

	// Create PawScript instance
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(optLevel),
	})

	// Register standard library with console I/O
	ps.RegisterStandardLibraryWithIO([]string{}, sr.channels.GetIOConfig())

	// Execute in goroutine
	go func() {
		// Create restricted snapshot and execute
		snapshot := ps.CreateRestrictedSnapshot()
		ps.ExecuteWithEnvironment(string(content), snapshot, filePath, 0, 0)

		// Flush output
		sr.channels.Flush()

		// Notify script end
		if sr.OnScriptEnd != nil {
			sr.OnScriptEnd()
		}

		// Call completion callback
		if onComplete != nil {
			onComplete()
		}

		// Restart REPL with fresh state
		sr.CreateREPL(false) // Don't show banner again
		sr.StartREPL()
	}()
}

// CreatePawScriptInstance creates a new PawScript instance configured for script execution.
func CreatePawScriptInstance(filePath string, optLevel int) *pawscript.PawScript {
	scriptDir := filepath.Dir(filePath)
	fileAccess := CreateFileAccessConfig(scriptDir)

	return pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		FileAccess:           fileAccess,
		ScriptDir:            scriptDir,
		OptLevel:             pawscript.OptimizationLevel(optLevel),
	})
}
