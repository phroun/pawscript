package pawscript

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// syncWriter serializes writes so stdout and stderr (pointed at the same buffer)
// interleave safely even when fibers write concurrently — mirroring the shell's
// `2>&1` merge that generated the .expected files.
type syncWriter struct {
	mu  sync.Mutex
	buf *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// corpusFileAccess reproduces the default sandbox the `paw` binary builds for a
// bare `paw file.paw` run from the fixture directory (cwd == scriptDir == dir).
func corpusFileAccess(dir string) *FileAccessConfig {
	tmp := os.TempDir()
	return &FileAccessConfig{
		ReadRoots:    []string{dir, tmp},
		WriteRoots:   []string{filepath.Join(dir, "saves"), filepath.Join(dir, "output"), tmp},
		ExecRoots:    []string{filepath.Join(dir, "helpers"), filepath.Join(dir, "bin")},
		IncludeRoots: []string{dir},
	}
}

// TestCorpus runs every tests/*.paw that has a matching *.expected through the
// interpreter in-process and compares combined stdout+stderr, putting the whole
// fixture corpus under `go test`/-race/coverage (it otherwise only runs via the
// shell script against a prebuilt binary).
func TestCorpus(t *testing.T) {
	testsDir, err := filepath.Abs("../tests")
	if err != nil {
		t.Fatal(err)
	}
	expectedFiles, err := filepath.Glob(filepath.Join(testsDir, "*.expected"))
	if err != nil {
		t.Fatal(err)
	}
	if len(expectedFiles) == 0 {
		t.Fatalf("no .expected fixtures found under %s", testsDir)
	}

	// These fixtures exercise output paths that write directly to os.Stdout/
	// os.Stderr, bypassing the configured Config.Stdout/Stderr, so their captured
	// output is unavoidably incomplete in-process. The bypass itself is a tracked
	// bug (see docs/code-review-2026-07.md); until it's fixed these can only be
	// verified via the shell script against the binary.
	skip := map[string]string{
		"test_scope_operator.paw":  "module.go emits 'Module not found' straight to os.Stderr",
		"test_terminal_cursor.paw": "terminal.go emits the reset sequence via fmt.Print (os.Stdout)",
	}

	for _, expPath := range expectedFiles {
		pawPath := expPath[:len(expPath)-len(".expected")] + ".paw"
		name := filepath.Base(pawPath)
		t.Run(name, func(t *testing.T) {
			if reason, skipped := skip[name]; skipped {
				t.Skipf("not reproducible in-process: %s", reason)
			}
			paw, err := os.ReadFile(pawPath)
			if err != nil {
				t.Skipf("no .paw for %s: %v", filepath.Base(expPath), err)
			}
			want, err := os.ReadFile(expPath)
			if err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			w := &syncWriter{buf: &buf}
			ps := New(&Config{
				Stdout:               w,
				Stderr:               w,
				AllowMacros:          true,
				EnableSyntacticSugar: true,
				ShowErrorContext:     true,
				ContextLines:         2,
				ScriptDir:            testsDir,
				FileAccess:           corpusFileAccess(testsDir),
				OptLevel:             OptimizeBasic, // match the paw binary's default (-O1)
			})
			ps.RegisterStandardLibrary(nil)

			// The regression script invokes `../paw file.paw` from the tests dir, so
			// diagnostics show the bare filename — match that, not an absolute path.
			result := ps.ExecuteFile(string(paw), name)

			// If the script returned a token, async work is pending — wait for it
			// to drain (the binary polls the same way).
			if _, ok := result.(TokenResult); ok {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					status := ps.GetTokenStatus()
					if ac, _ := status["activeCount"].(int); ac == 0 {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}

			if got := buf.String(); got != string(want) {
				t.Errorf("output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}
