package pawscript

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeExe writes an executable file at dir/name and returns its path.
func makeExe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestValidateExecAccess_UnrestrictedWhenNoSandbox: FileAccess nil => exec is
// unrestricted (the --unrestricted / embedder-opt-out path).
func TestValidateExecAccess_UnrestrictedWhenNoSandbox(t *testing.T) {
	if _, err := validateExecAccess(&Config{}, "anything", "/no/such/binary"); err != nil {
		t.Errorf("nil FileAccess should allow exec unrestricted, got %v", err)
	}
	if _, err := validateExecAccess(nil, "anything", "/no/such/binary"); err != nil {
		t.Errorf("nil config should allow exec unrestricted, got %v", err)
	}
}

// TestValidateExecAccess_DenyByDefault: the core of Model B — with a FileAccess
// sandbox present but no ExecRoots, exec is denied (fail closed), for both nil
// and empty ExecRoots.
func TestValidateExecAccess_DenyByDefault(t *testing.T) {
	bin := t.TempDir()
	exe := makeExe(t, bin, "tool")

	for _, tc := range []struct {
		name  string
		roots []string
	}{
		{"nil ExecRoots", nil},
		{"empty ExecRoots", []string{}},
	} {
		cfg := &Config{FileAccess: &FileAccessConfig{ExecRoots: tc.roots}}
		_, err := validateExecAccess(cfg, "tool", exe)
		if err == nil {
			t.Errorf("%s: expected deny-by-default, got allowed", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "exec is disabled") {
			t.Errorf("%s: expected 'exec is disabled' error, got %v", tc.name, err)
		}
	}
}

// TestValidateExecAccess_AllowedWithinRoot: an absolute command inside an
// explicitly-listed ExecRoot is permitted.
func TestValidateExecAccess_AllowedWithinRoot(t *testing.T) {
	bin := t.TempDir()
	exe := makeExe(t, bin, "tool")
	cfg := &Config{FileAccess: &FileAccessConfig{ExecRoots: []string{bin}}}
	if _, err := validateExecAccess(cfg, "tool", exe); err != nil {
		t.Errorf("expected exec allowed within listed root, got %v", err)
	}
}

// TestValidateExecAccess_OutsideRootDenied: a command outside all ExecRoots is
// denied.
func TestValidateExecAccess_OutsideRootDenied(t *testing.T) {
	bin := t.TempDir()
	other := t.TempDir()
	exe := makeExe(t, other, "tool") // lives outside `bin`
	cfg := &Config{FileAccess: &FileAccessConfig{ExecRoots: []string{bin}}}
	_, err := validateExecAccess(cfg, "tool", exe)
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Errorf("expected 'outside allowed roots' denial, got %v", err)
	}
}

// TestValidateExecAccess_WriteExecOverlapDenied: a command whose directory is
// both an ExecRoot and a WriteRoot is denied (write-then-execute guard).
func TestValidateExecAccess_WriteExecOverlapDenied(t *testing.T) {
	bin := t.TempDir()
	exe := makeExe(t, bin, "tool")
	cfg := &Config{FileAccess: &FileAccessConfig{
		ExecRoots:  []string{bin},
		WriteRoots: []string{bin}, // same dir is writable -> write-then-execute
	}}
	_, err := validateExecAccess(cfg, "tool", exe)
	if err == nil || !strings.Contains(err.Error(), "writable directory") {
		t.Errorf("expected write-then-execute denial, got %v", err)
	}
}

// TestValidateExecAccess_NotFound: an absolute command that does not exist is
// reported as not found (only reached once ExecRoots is non-empty).
func TestValidateExecAccess_NotFound(t *testing.T) {
	bin := t.TempDir()
	cfg := &Config{FileAccess: &FileAccessConfig{ExecRoots: []string{bin}}}
	_, err := validateExecAccess(cfg, "ghost", filepath.Join(bin, "ghost"))
	if err == nil || !strings.Contains(err.Error(), "command not found") {
		t.Errorf("expected 'command not found', got %v", err)
	}
}

// TestValidateExecAccess_PathLookup exercises the PATH-lookup branch with a real
// system binary, gating it by the directory it actually resolves to.
func TestValidateExecAccess_PathLookup(t *testing.T) {
	real, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo on PATH")
	}
	dir := filepath.Dir(real)

	// ExecRoots contains echo's real directory -> allowed.
	cfg := &Config{FileAccess: &FileAccessConfig{ExecRoots: []string{dir}}}
	if _, err := validateExecAccess(cfg, "echo", "echo"); err != nil {
		t.Errorf("expected echo allowed when its dir is an ExecRoot, got %v", err)
	}

	// ExecRoots elsewhere -> denied.
	cfg2 := &Config{FileAccess: &FileAccessConfig{ExecRoots: []string{t.TempDir()}}}
	if _, err := validateExecAccess(cfg2, "echo", "echo"); err == nil {
		t.Error("expected echo denied when its dir is not an ExecRoot")
	}
}
