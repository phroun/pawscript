package pawscript

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// pathHasPrefix checks if path starts with prefix, handling case sensitivity
// based on the operating system's file system conventions
func pathHasPrefix(path, prefix string) bool {
	// Windows and macOS (darwin) typically have case-insensitive file systems
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
	}
	return strings.HasPrefix(path, prefix)
}

// pathEquals checks if two paths are equal, handling case sensitivity
// based on the operating system's file system conventions
func pathEquals(path1, path2 string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(path1, path2)
	}
	return path1 == path2
}

// RegisterFilesLib registers file system commands
// Module: files
func (ps *PawScript) RegisterFilesLib() {
	// Helper to set a StoredList as result
	setListResult := func(ctx *Context, list StoredList) {
		id := ctx.executor.storeObject(list, "list")
		marker := fmt.Sprintf("\x00LIST:%d\x00", id)
		ctx.state.SetResultWithoutClaim(Symbol(marker))
	}

	// Helper to validate path access against configured roots
	// Returns cleaned absolute path and nil error if allowed
	validatePathAccess := func(ctx *Context, path string, needsWrite bool) (string, error) {
		// Get absolute path - resolve relative paths from ScriptDir if available
		var absPath string
		var err error
		if !filepath.IsAbs(path) && ps.config != nil && ps.config.ScriptDir != "" {
			// Resolve relative path from script directory
			absPath = filepath.Join(ps.config.ScriptDir, path)
		} else {
			absPath, err = filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("invalid path: %v", err)
			}
		}
		absPath = filepath.Clean(absPath)

		// Get file access config from PawScript instance
		if ps.config == nil || ps.config.FileAccess == nil {
			// No restrictions configured
			return absPath, nil
		}

		fileAccess := ps.config.FileAccess

		// Check write roots if write access needed
		if needsWrite {
			if fileAccess.WriteRoots == nil {
				// nil means unrestricted
				return absPath, nil
			}
			if len(fileAccess.WriteRoots) == 0 {
				// Empty slice means no write access allowed
				return "", fmt.Errorf("write access denied: no write roots configured")
			}
			allowed := false
			for _, root := range fileAccess.WriteRoots {
				absRoot, err := filepath.Abs(root)
				if err != nil {
					continue
				}
				absRoot = filepath.Clean(absRoot)
				// Use case-insensitive comparison on Windows/macOS
				if pathHasPrefix(absPath, absRoot+string(filepath.Separator)) || pathEquals(absPath, absRoot) {
					allowed = true
					break
				}
			}
			if !allowed {
				return "", fmt.Errorf("write access denied: path outside allowed roots")
			}
		} else {
			// Check read roots
			if fileAccess.ReadRoots == nil {
				// nil means unrestricted
				return absPath, nil
			}
			if len(fileAccess.ReadRoots) == 0 {
				// Empty slice means no read access allowed
				return "", fmt.Errorf("read access denied: no read roots configured")
			}
			allowed := false
			for _, root := range fileAccess.ReadRoots {
				absRoot, err := filepath.Abs(root)
				if err != nil {
					continue
				}
				absRoot = filepath.Clean(absRoot)
				// Use case-insensitive comparison on Windows/macOS
				if pathHasPrefix(absPath, absRoot+string(filepath.Separator)) || pathEquals(absPath, absRoot) {
					allowed = true
					break
				}
			}
			if !allowed {
				return "", fmt.Errorf("read access denied: path outside allowed roots")
			}
		}

		return absPath, nil
	}

	// Helper to resolve a file from an argument
	resolveFile := func(ctx *Context, arg interface{}) *StoredFile {
		switch v := arg.(type) {
		case *StoredFile:
			return v
		case Symbol:
			symStr := string(v)
			if strings.HasPrefix(symStr, "#") {
				// Try to resolve from local vars then module
				if value, exists := ctx.state.GetVariable(symStr); exists {
					if f, ok := value.(*StoredFile); ok {
						return f
					}
				}
				if ctx.state.moduleEnv != nil {
					ctx.state.moduleEnv.mu.RLock()
					defer ctx.state.moduleEnv.mu.RUnlock()
					if ctx.state.moduleEnv.ObjectsModule != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsModule[symStr]; exists {
							if f, ok := obj.(*StoredFile); ok {
								return f
							}
						}
					}
					if ctx.state.moduleEnv.ObjectsInherited != nil {
						if obj, exists := ctx.state.moduleEnv.ObjectsInherited[symStr]; exists {
							if f, ok := obj.(*StoredFile); ok {
								return f
							}
						}
					}
				}
			}
		}
		return nil
	}

	// ==================== files:: module ====================

	// file - Open a file
	// Usage: file <path> [mode: "r"|"w"|"a"|"rw"] [create: true]
	// Returns a file handle
	ps.RegisterCommandInModule("files", "file", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "file: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		mode := "r"
		createIfMissing := false

		// Check named args
		if m, ok := ctx.NamedArgs["mode"]; ok {
			mode = fmt.Sprintf("%v", m)
		}
		if c, ok := ctx.NamedArgs["create"]; ok {
			if b, ok := c.(bool); ok {
				createIfMissing = b
			} else if s, ok := c.(string); ok {
				createIfMissing = s == "true"
			}
		}

		// Determine if write access is needed
		needsWrite := mode == "w" || mode == "a" || mode == "rw"

		// Validate path access
		absPath, err := validatePathAccess(ctx, path, needsWrite)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file: %v", err))
			return BoolStatus(false)
		}

		// Determine OS flags based on mode
		var flags int
		switch mode {
		case "r":
			flags = os.O_RDONLY
		case "w":
			flags = os.O_WRONLY | os.O_TRUNC
			if createIfMissing {
				flags |= os.O_CREATE
			}
		case "a":
			flags = os.O_WRONLY | os.O_APPEND
			if createIfMissing {
				flags |= os.O_CREATE
			}
		case "rw":
			flags = os.O_RDWR
			if createIfMissing {
				flags |= os.O_CREATE
			}
		default:
			ctx.LogError(CatCommand, fmt.Sprintf("file: invalid mode '%s' (use r, w, a, or rw)", mode))
			return BoolStatus(false)
		}

		// Open the file
		file, err := os.OpenFile(absPath, flags, 0644)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file: %v", err))
			return BoolStatus(false)
		}

		// Create StoredFile and return
		storedFile := NewStoredFile(file, path, mode)
		ctx.SetResult(storedFile)
		return BoolStatus(true)
	})

	// close - Close a file
	// Usage: close <file>
	ps.RegisterCommandInModule("files", "close", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "close: file required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "close: not a file handle")
			return BoolStatus(false)
		}

		err := file.Close()
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("close: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// seek - Seek to position in file
	// Usage: seek <file> <offset> [from: "start"|"current"|"end"]
	ps.RegisterCommandInModule("files", "seek", func(ctx *Context) Result {
		if len(ctx.Args) < 2 {
			ctx.LogError(CatCommand, "seek: file and offset required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "seek: not a file handle")
			return BoolStatus(false)
		}

		offset, ok := toInt64(ctx.Args[1])
		if !ok {
			ctx.LogError(CatCommand, "seek: offset must be a number")
			return BoolStatus(false)
		}

		whence := io.SeekStart
		if from, ok := ctx.NamedArgs["from"]; ok {
			switch fmt.Sprintf("%v", from) {
			case "start":
				whence = io.SeekStart
			case "current":
				whence = io.SeekCurrent
			case "end":
				whence = io.SeekEnd
			default:
				ctx.LogError(CatCommand, "seek: from must be start, current, or end")
				return BoolStatus(false)
			}
		}

		pos, err := file.Seek(offset, whence)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("seek: %v", err))
			return BoolStatus(false)
		}

		ctx.SetResult(pos)
		return BoolStatus(true)
	})

	// tell - Get current file position
	// Usage: tell <file>
	ps.RegisterCommandInModule("files", "tell", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "tell: file required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "tell: not a file handle")
			return BoolStatus(false)
		}

		pos, err := file.Tell()
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("tell: %v", err))
			return BoolStatus(false)
		}

		ctx.SetResult(pos)
		return BoolStatus(true)
	})

	// flush - Flush file buffers
	// Usage: flush <file>
	ps.RegisterCommandInModule("files", "flush", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "flush: file required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "flush: not a file handle")
			return BoolStatus(false)
		}

		err := file.Flush()
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("flush: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// truncate - Truncate file at current position
	// Usage: truncate <file>
	ps.RegisterCommandInModule("files", "truncate", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "truncate: file required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "truncate: not a file handle")
			return BoolStatus(false)
		}

		err := file.Truncate()
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("truncate: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// file_close - Explicitly close a file handle
	// Usage: file_close <file>
	// Closes the file immediately, even if other threads hold references.
	// After closing, the handle becomes invalid and further operations will fail.
	ps.RegisterCommandInModule("files", "file_close", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "file_close: file required")
			return BoolStatus(false)
		}

		file := resolveFile(ctx, ctx.Args[0])
		if file == nil {
			ctx.LogError(CatCommand, "file_close: not a file handle")
			return BoolStatus(false)
		}

		err := file.Close()
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file_close: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// ==================== Path/Directory Operations (no handle needed) ====================

	// file_exists - Check if a path exists
	// Usage: file_exists <path>
	ps.RegisterCommandInModule("files", "file_exists", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "file_exists: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])

		// Validate read access
		absPath, err := validatePathAccess(ctx, path, false)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file_exists: %v", err))
			return BoolStatus(false)
		}

		_, err = os.Stat(absPath)
		ctx.SetResult(err == nil)
		return BoolStatus(true)
	})

	// file_info - Get file/directory information
	// Usage: file_info <path>
	// Returns: (size: N, mtime: T, isdir: bool, mode: "rwxr-xr-x")
	ps.RegisterCommandInModule("files", "file_info", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "file_info: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])

		// Validate read access
		absPath, err := validatePathAccess(ctx, path, false)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file_info: %v", err))
			return BoolStatus(false)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("file_info: %v", err))
			return BoolStatus(false)
		}

		// Build result list with named args
		namedArgs := map[string]interface{}{
			"size":  info.Size(),
			"mtime": info.ModTime().Unix(),
			"isdir": info.IsDir(),
			"mode":  info.Mode().String(),
		}
		result := NewStoredListWithNamed(nil, namedArgs)

		setListResult(ctx, result)
		return BoolStatus(true)
	})

	// list_dir - List directory contents
	// Usage: list_dir <path>
	// Returns: list of filenames
	ps.RegisterCommandInModule("files", "list_dir", func(ctx *Context) Result {
		path := "."
		if len(ctx.Args) > 0 {
			path = fmt.Sprintf("%v", ctx.Args[0])
		}

		// Validate read access
		absPath, err := validatePathAccess(ctx, path, false)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("list_dir: %v", err))
			return BoolStatus(false)
		}

		entries, err := os.ReadDir(absPath)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("list_dir: %v", err))
			return BoolStatus(false)
		}

		var items []interface{}
		for _, entry := range entries {
			items = append(items, entry.Name())
		}

		setListResult(ctx, NewStoredListWithoutRefs(items))
		return BoolStatus(true)
	})

	// mkdir - Create directory
	// Usage: mkdir <path> [parents: true]
	ps.RegisterCommandInModule("files", "mkdir", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "mkdir: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		parents := false

		if p, ok := ctx.NamedArgs["parents"]; ok {
			if b, ok := p.(bool); ok {
				parents = b
			} else if s, ok := p.(string); ok {
				parents = s == "true"
			}
		}

		// Validate write access
		absPath, err := validatePathAccess(ctx, path, true)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("mkdir: %v", err))
			return BoolStatus(false)
		}

		if parents {
			err = os.MkdirAll(absPath, 0755)
		} else {
			err = os.Mkdir(absPath, 0755)
		}

		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("mkdir: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// rm - Remove file
	// Usage: rm <path>
	ps.RegisterCommandInModule("files", "rm", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "rm: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])

		// Validate write access
		absPath, err := validatePathAccess(ctx, path, true)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("rm: %v", err))
			return BoolStatus(false)
		}

		err = os.Remove(absPath)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("rm: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// rmdir - Remove directory
	// Usage: rmdir <path> [recursive: true]
	ps.RegisterCommandInModule("files", "rmdir", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "rmdir: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		recursive := false

		if r, ok := ctx.NamedArgs["recursive"]; ok {
			if b, ok := r.(bool); ok {
				recursive = b
			} else if s, ok := r.(string); ok {
				recursive = s == "true"
			}
		}

		// Validate write access
		absPath, err := validatePathAccess(ctx, path, true)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("rmdir: %v", err))
			return BoolStatus(false)
		}

		if recursive {
			err = os.RemoveAll(absPath)
		} else {
			err = os.Remove(absPath)
		}

		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("rmdir: %v", err))
			return BoolStatus(false)
		}

		return BoolStatus(true)
	})

	// ==================== Path Manipulation (pure, no filesystem access) ====================

	// abs_path - Get absolute path
	// Usage: abs_path <path>
	ps.RegisterCommandInModule("files", "abs_path", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "abs_path: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		absPath, err := filepath.Abs(path)
		if err != nil {
			ctx.LogError(CatCommand, fmt.Sprintf("abs_path: %v", err))
			return BoolStatus(false)
		}

		ctx.SetResult(absPath)
		return BoolStatus(true)
	})

	// join_path - Join path components
	// Usage: join_path <parts...>
	ps.RegisterCommandInModule("files", "join_path", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "join_path: at least one path component required")
			return BoolStatus(false)
		}

		var parts []string
		for _, arg := range ctx.Args {
			parts = append(parts, fmt.Sprintf("%v", arg))
		}

		ctx.SetResult(filepath.Join(parts...))
		return BoolStatus(true)
	})

	// dir_name - Get directory portion of path
	// Usage: dir_name <path>
	ps.RegisterCommandInModule("files", "dir_name", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "dir_name: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(filepath.Dir(path))
		return BoolStatus(true)
	})

	// base_name - Get filename portion of path
	// Usage: base_name <path>
	ps.RegisterCommandInModule("files", "base_name", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "base_name: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(filepath.Base(path))
		return BoolStatus(true)
	})

	// file_ext - Get file extension
	// Usage: file_ext <path>
	ps.RegisterCommandInModule("files", "file_ext", func(ctx *Context) Result {
		if len(ctx.Args) < 1 {
			ctx.LogError(CatCommand, "file_ext: path required")
			return BoolStatus(false)
		}

		path := fmt.Sprintf("%v", ctx.Args[0])
		ctx.SetResult(filepath.Ext(path))
		return BoolStatus(true)
	})
}

// Suppress unused import warning for time
var _ = time.Now
