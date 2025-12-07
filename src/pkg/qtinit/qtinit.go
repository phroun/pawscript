// Package qtinit sets Qt environment variables before any Qt code runs.
// This package MUST be imported first, before any therecipe/qt packages.
// Import it with a blank identifier: import _ "github.com/phroun/pawscript/pkg/qtinit"
package qtinit

import (
	"os"
	"path/filepath"
	"runtime"
)

func init() {
	// Set Qt environment variables before Qt initializes to avoid screen scaling issues
	// These must be set before any Qt code runs
	if os.Getenv("QT_AUTO_SCREEN_SCALE_FACTOR") == "" {
		os.Setenv("QT_AUTO_SCREEN_SCALE_FACTOR", "0")
	}
	if os.Getenv("QT_SCALE_FACTOR") == "" {
		os.Setenv("QT_SCALE_FACTOR", "1")
	}
	if os.Getenv("QT_SCREEN_SCALE_FACTORS") == "" {
		os.Setenv("QT_SCREEN_SCALE_FACTORS", "1")
	}
	// Disable high DPI scaling which can cause divide-by-zero in therecipe/qt
	if os.Getenv("QT_ENABLE_HIGHDPI_SCALING") == "" {
		os.Setenv("QT_ENABLE_HIGHDPI_SCALING", "0")
	}
	// Disable device pixel ratio rounding
	if os.Getenv("QT_DEVICE_PIXEL_RATIO") == "" {
		os.Setenv("QT_DEVICE_PIXEL_RATIO", "1")
	}

	// Set up Qt paths based on platform
	if runtime.GOOS == "darwin" {
		// macOS: try common Homebrew Qt5 locations
		qtPaths := []string{
			"/opt/homebrew/opt/qt@5",  // Apple Silicon Homebrew
			"/usr/local/opt/qt@5",      // Intel Homebrew
			"/opt/homebrew/opt/qt",     // Qt6 on Apple Silicon
			"/usr/local/opt/qt",        // Qt6 on Intel
		}
		for _, qtPath := range qtPaths {
			if _, err := os.Stat(qtPath); err == nil {
				if os.Getenv("QT_DIR") == "" {
					os.Setenv("QT_DIR", qtPath)
				}
				if os.Getenv("QT_PLUGIN_PATH") == "" {
					os.Setenv("QT_PLUGIN_PATH", filepath.Join(qtPath, "plugins"))
				}
				if os.Getenv("QT_QPA_PLATFORM_PLUGIN_PATH") == "" {
					os.Setenv("QT_QPA_PLATFORM_PLUGIN_PATH", filepath.Join(qtPath, "plugins", "platforms"))
				}
				// On macOS, ensure we use the cocoa platform
				if os.Getenv("QT_QPA_PLATFORM") == "" {
					os.Setenv("QT_QPA_PLATFORM", "cocoa")
				}
				break
			}
		}
	} else if runtime.GOOS == "linux" {
		// Linux: Qt plugins are usually in standard locations
		pluginPaths := []string{
			"/usr/lib/x86_64-linux-gnu/qt5/plugins",
			"/usr/lib/qt5/plugins",
			"/usr/lib64/qt5/plugins",
		}
		for _, pluginPath := range pluginPaths {
			if _, err := os.Stat(pluginPath); err == nil {
				if os.Getenv("QT_PLUGIN_PATH") == "" {
					os.Setenv("QT_PLUGIN_PATH", pluginPath)
				}
				break
			}
		}
	}
}
