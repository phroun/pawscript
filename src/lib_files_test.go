package pawscript

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cfgWithRoots builds a Config whose FileAccess restricts read and write to the
// single root, with the given FollowSymlinks setting.
func cfgWithRoots(root string, follow bool) *Config {
	return &Config{
		FileAccess: &FileAccessConfig{
			ReadRoots:      []string{root},
			WriteRoots:     []string{root},
			FollowSymlinks: follow,
		},
	}
}

// skipIfNoSymlink creates a symlink and skips the test if the platform won't
// allow it (e.g. unprivileged Windows).
func skipIfNoSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}
}

func TestValidateFileAccess_NormalFileInsideRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "data.txt")
	if err := os.WriteFile(inside, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, follow := range []bool{false, true} {
		cfg := cfgWithRoots(root, follow)
		if _, err := validateFileAccess(cfg, inside, false); err != nil {
			t.Errorf("follow=%v: expected read allowed for in-root file, got %v", follow, err)
		}
		if _, err := validateFileAccess(cfg, inside, true); err != nil {
			t.Errorf("follow=%v: expected write allowed for in-root file, got %v", follow, err)
		}
	}
}

func TestValidateFileAccess_PathOutsideRootDenied(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateFileAccess(cfgWithRoots(root, false), outside, false); err == nil {
		t.Fatal("expected denial for path textually outside root")
	}
}

// TestValidateFileAccess_SymlinkEscape is the core of the feature: a symlink
// inside the root that points outside it must be blocked when FollowSymlinks is
// off and permitted when it is on.
func TestValidateFileAccess_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape") // root/escape -> outsideDir
	skipIfNoSymlink(t, outsideDir, link)

	target := filepath.Join(link, "secret.txt") // resolves to outsideDir/secret.txt

	// FollowSymlinks=false (default, secure): escape is denied for read and write.
	if _, err := validateFileAccess(cfgWithRoots(root, false), target, false); err == nil {
		t.Error("FollowSymlinks=false: expected symlink escape denied for read")
	}
	if _, err := validateFileAccess(cfgWithRoots(root, false), target, true); err == nil {
		t.Error("FollowSymlinks=false: expected symlink escape denied for write")
	}
	// FollowSymlinks=true: the operator opts into trusting root contents.
	if _, err := validateFileAccess(cfgWithRoots(root, true), target, false); err != nil {
		t.Errorf("FollowSymlinks=true: expected symlink followed, got %v", err)
	}
}

// TestValidateFileAccess_InJailSymlinkAllowed verifies a symlink that stays
// within the jail is permitted in both modes — the guard only blocks escapes.
func TestValidateFileAccess_InJailSymlinkAllowed(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link") // root/link -> root/sub (in-jail)
	skipIfNoSymlink(t, sub, link)

	target := filepath.Join(link, "f.txt")
	for _, follow := range []bool{false, true} {
		if _, err := validateFileAccess(cfgWithRoots(root, follow), target, false); err != nil {
			t.Errorf("follow=%v: expected in-jail symlink allowed, got %v", follow, err)
		}
	}
}

// TestValidateFileAccess_CreateThroughEscapingParentDenied covers the write/create
// case where the leaf does not exist yet but a parent component is an escaping
// symlink: the parent must be resolved and rejected.
func TestValidateFileAccess_CreateThroughEscapingParentDenied(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	link := filepath.Join(root, "escape") // root/escape -> outsideDir
	skipIfNoSymlink(t, outsideDir, link)

	newFile := filepath.Join(link, "newfile.txt") // not created; resolves under outsideDir
	if _, err := validateFileAccess(cfgWithRoots(root, false), newFile, true); err == nil {
		t.Error("expected create through escaping parent symlink to be denied")
	}
	// A brand-new leaf directly inside the real root must still be creatable.
	okFile := filepath.Join(root, "newfile.txt")
	if _, err := validateFileAccess(cfgWithRoots(root, false), okFile, true); err != nil {
		t.Errorf("expected create of new leaf inside root allowed, got %v", err)
	}
}

// TestValidateFileAccess_LeafSymlinkEscapeDeniedForWrite makes sure writing
// through a leaf symlink that targets an outside file (which would clobber it)
// is blocked.
func TestValidateFileAccess_LeafSymlinkEscapeDeniedForWrite(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leaf") // root/leaf -> outsideDir/target.txt
	skipIfNoSymlink(t, outsideFile, link)

	if _, err := validateFileAccess(cfgWithRoots(root, false), link, true); err == nil {
		t.Error("expected write through escaping leaf symlink to be denied")
	}
}

// TestValidateFileAccess_UnrestrictedNilRoots confirms nil roots mean
// unrestricted (symlink guard does not apply) and empty roots mean deny-all.
func TestValidateFileAccess_RootSemantics(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "f.txt")
	if err := os.WriteFile(inside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// nil FileAccess -> unrestricted.
	if _, err := validateFileAccess(&Config{}, "/etc/hostname", false); err != nil {
		t.Errorf("nil FileAccess should be unrestricted, got %v", err)
	}
	// nil ReadRoots -> unrestricted read.
	cfgNil := &Config{FileAccess: &FileAccessConfig{ReadRoots: nil}}
	if _, err := validateFileAccess(cfgNil, "/etc/hostname", false); err != nil {
		t.Errorf("nil ReadRoots should be unrestricted, got %v", err)
	}
	// empty ReadRoots -> deny-all read.
	cfgEmpty := &Config{FileAccess: &FileAccessConfig{ReadRoots: []string{}}}
	if _, err := validateFileAccess(cfgEmpty, inside, false); err == nil {
		t.Error("empty ReadRoots should deny all read access")
	}
}

// TestValidateIncludeAccess_RootSemantics: nil IncludeRoots = unrestricted
// (back-compat), empty = deny-all, listed = allow only within.
func TestValidateIncludeAccess_RootSemantics(t *testing.T) {
	app := t.TempDir()
	mod := filepath.Join(app, "mod.paw")
	if err := os.WriteFile(mod, []byte("print \"hi\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// nil FileAccess -> unrestricted.
	if _, err := validateIncludeAccess(&Config{}, mod); err != nil {
		t.Errorf("nil FileAccess should allow include, got %v", err)
	}
	// nil IncludeRoots (sandbox set, include roots unset) -> unrestricted (back-compat).
	cfgNil := &Config{FileAccess: &FileAccessConfig{ReadRoots: []string{app}}}
	if _, err := validateIncludeAccess(cfgNil, mod); err != nil {
		t.Errorf("nil IncludeRoots should be unrestricted, got %v", err)
	}
	// empty IncludeRoots -> deny-all.
	cfgEmpty := &Config{FileAccess: &FileAccessConfig{IncludeRoots: []string{}}}
	if _, err := validateIncludeAccess(cfgEmpty, mod); err == nil {
		t.Error("empty IncludeRoots should deny all includes")
	}
	// listed -> allowed within, denied outside.
	cfg := &Config{FileAccess: &FileAccessConfig{IncludeRoots: []string{app}}}
	if _, err := validateIncludeAccess(cfg, mod); err != nil {
		t.Errorf("expected include allowed within root, got %v", err)
	}
	outside := filepath.Join(t.TempDir(), "other.paw")
	if err := os.WriteFile(outside, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateIncludeAccess(cfg, outside); err == nil {
		t.Error("expected include outside root denied")
	}
}

// TestValidateIncludeAccess_IndependentOfReadRoots is the property the whole
// feature exists for: an app can load modules from its own folder while its data
// reads are confined to a different path. IncludeRoots and ReadRoots are checked
// independently — neither implies the other.
func TestValidateIncludeAccess_IndependentOfReadRoots(t *testing.T) {
	appDir := t.TempDir()  // where modules live
	dataDir := t.TempDir() // where data reads are confined
	mod := filepath.Join(appDir, "mod.paw")
	data := filepath.Join(dataDir, "data.txt")
	for _, p := range []string{mod, data} {
		if err := os.WriteFile(p, []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &Config{FileAccess: &FileAccessConfig{
		ReadRoots:    []string{dataDir}, // data only
		IncludeRoots: []string{appDir},  // modules only
	}}

	// A module in appDir: includable, but NOT readable as data.
	if _, err := validateIncludeAccess(cfg, mod); err != nil {
		t.Errorf("module in IncludeRoots should be includable, got %v", err)
	}
	if _, err := validateFileAccess(cfg, mod, false); err == nil {
		t.Error("module in appDir should NOT be readable as data (outside ReadRoots)")
	}

	// A data file in dataDir: readable, but NOT includable.
	if _, err := validateFileAccess(cfg, data, false); err != nil {
		t.Errorf("data in ReadRoots should be readable, got %v", err)
	}
	if _, err := validateIncludeAccess(cfg, data); err == nil {
		t.Error("data file should NOT be includable (outside IncludeRoots)")
	}
}

// TestValidateIncludeAccess_SymlinkEscape: the include sandbox honors the same
// symlink guard as file reads.
func TestValidateIncludeAccess_SymlinkEscape(t *testing.T) {
	app := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.paw"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(app, "escape")
	skipIfNoSymlink(t, outside, link)

	target := filepath.Join(link, "evil.paw") // resolves outside app
	cfg := &Config{FileAccess: &FileAccessConfig{IncludeRoots: []string{app}, FollowSymlinks: false}}
	if _, err := validateIncludeAccess(cfg, target); err == nil {
		t.Error("include through escaping symlink should be denied when FollowSymlinks=false")
	}
	cfgFollow := &Config{FileAccess: &FileAccessConfig{IncludeRoots: []string{app}, FollowSymlinks: true}}
	if _, err := validateIncludeAccess(cfgFollow, target); err != nil {
		t.Errorf("FollowSymlinks=true should permit the include, got %v", err)
	}
}

// TestNoSymlinkCreatingCommand is the invariant guard behind the FollowSymlinks
// option: following symlinks is only safe because a sandboxed script cannot
// CREATE a symlink. If a future change registers a link-creating command, this
// test fails so the security assumption is revisited before shipping.
func TestNoSymlinkCreatingCommand(t *testing.T) {
	ps := newTestPS()
	// Any command whose name contains "symlink", plus the unambiguous link-making
	// verbs. Deliberately excludes generic names like "ln" (natural log) and
	// "link" that are not symlink creators.
	isLinkMaker := func(name string) bool {
		l := strings.ToLower(name)
		return strings.Contains(l, "symlink") || l == "mklink" || l == "hardlink"
	}
	ps.rootModuleEnv.mu.RLock()
	defer ps.rootModuleEnv.mu.RUnlock()
	for module, section := range ps.rootModuleEnv.LibraryInherited {
		for name, item := range section {
			if item != nil && item.Type == "command" && isLinkMaker(name) {
				t.Errorf("found symlink-creating command %q in module %q; the FollowSymlinks "+
					"sandbox guarantee assumes scripts cannot create symlinks", name, module)
			}
		}
	}
}
