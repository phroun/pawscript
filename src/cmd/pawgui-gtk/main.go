// pawgui-gtk - GTK3-based GUI for PawScript with VTE terminal
// This is a proof of concept alternative to the Fyne-based GUI
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/sqp/vte"
)

// #cgo pkg-config: gtk+-3.0 vte-2.91
// #include <gtk/gtk.h>
// #include <vte/vte.h>
import "C"

const (
	appID   = "com.pawscript.pawgui-gtk"
	appName = "PawScript Launcher (GTK)"
)

// Global state
var (
	currentDir string
	mainWindow *gtk.ApplicationWindow
	fileList   *gtk.ListBox
	terminal   *vte.Terminal
	pathLabel  *gtk.Label
)

func main() {
	app, err := gtk.ApplicationNew(appID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create application: %v\n", err)
		os.Exit(1)
	}

	app.Connect("activate", func() {
		activate(app)
	})

	os.Exit(app.Run(os.Args))
}

func activate(app *gtk.Application) {
	// Create main window
	var err error
	mainWindow, err = gtk.ApplicationWindowNew(app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create window: %v\n", err)
		return
	}
	mainWindow.SetTitle(appName)
	mainWindow.SetDefaultSize(900, 600)

	// Create main horizontal paned (split view)
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	paned.SetPosition(300)

	// Left panel: File browser
	leftPanel := createFileBrowser()
	paned.Pack1(leftPanel, false, false)

	// Right panel: Terminal
	rightPanel := createTerminal()
	paned.Pack2(rightPanel, true, false)

	mainWindow.Add(paned)

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()
	updatePathLabel()

	// Print welcome message
	terminal.Feed("PawScript Launcher (GTK3 + VTE)\r\n")
	terminal.Feed("Select a .paw file and click Run to execute.\r\n\r\n")

	mainWindow.ShowAll()
}

func getDefaultDir() string {
	// Try to find examples directory
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		examples := filepath.Join(exeDir, "examples")
		if info, err := os.Stat(examples); err == nil && info.IsDir() {
			return examples
		}
	}
	// Fall back to current directory
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func createFileBrowser() *gtk.Box {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	box.SetMarginStart(5)
	box.SetMarginEnd(5)
	box.SetMarginTop(5)
	box.SetMarginBottom(5)

	// Directory label
	dirLabel, _ := gtk.LabelNew("")
	dirLabel.SetXAlign(0)
	dirLabel.SetMarkup("<b>Directory:</b>")
	box.PackStart(dirLabel, false, false, 0)

	// Current path label
	pathLabel, _ = gtk.LabelNew(currentDir)
	pathLabel.SetXAlign(0)
	pathLabel.SetLineWrap(true)
	pathLabel.SetSelectable(true)
	box.PackStart(pathLabel, false, false, 0)

	// Scrolled window for file list
	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroll.SetVExpand(true)

	// File list
	fileList, _ = gtk.ListBoxNew()
	fileList.SetSelectionMode(gtk.SELECTION_SINGLE)
	fileList.SetActivateOnSingleClick(false)
	fileList.Connect("row-activated", onFileActivated)
	scroll.Add(fileList)
	box.PackStart(scroll, true, true, 0)

	// Button box
	buttonBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)

	runButton, _ := gtk.ButtonNewWithLabel("Run")
	runButton.Connect("clicked", onRunClicked)
	runButton.SetHExpand(true)
	buttonBox.PackStart(runButton, true, true, 0)

	browseButton, _ := gtk.ButtonNewWithLabel("Browse...")
	browseButton.Connect("clicked", onBrowseClicked)
	browseButton.SetHExpand(true)
	buttonBox.PackStart(browseButton, true, true, 0)

	box.PackStart(buttonBox, false, false, 0)

	return box
}

func createTerminal() *gtk.Box {
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Label
	label, _ := gtk.LabelNew("")
	label.SetXAlign(0)
	label.SetMarkup("<b>Console Output:</b>")
	label.SetMarginStart(5)
	label.SetMarginTop(5)
	box.PackStart(label, false, false, 0)

	// VTE Terminal (has built-in scrolling)
	terminal = vte.NewTerminal()

	// Dark color scheme
	terminal.SetBgColorFromString("#1e1e1e")
	terminal.SetFgColorFromString("#d4d4d4")

	// Set font
	terminal.SetFontFromString("Monospace 11")

	// Set scrollback
	terminal.SetScrollbackLines(10000)

	// Add terminal widget directly to box using C interop
	// VteTerminal is a GtkWidget, so we can add it to the container
	nativePtr := terminal.Native()
	C.gtk_box_pack_start(
		(*C.GtkBox)(unsafe.Pointer(box.Native())),
		(*C.GtkWidget)(unsafe.Pointer(nativePtr)),
		C.TRUE,  // expand
		C.TRUE,  // fill
		0,       // padding
	)

	return box
}

func updatePathLabel() {
	if pathLabel != nil {
		pathLabel.SetText(currentDir)
	}
}

func refreshFileList() {
	// Clear existing items
	children := fileList.GetChildren()
	children.Foreach(func(item interface{}) {
		if widget, ok := item.(*gtk.Widget); ok {
			fileList.Remove(widget)
		}
	})

	// Read directory
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error reading directory: %v\r\n", err))
		return
	}

	// Add parent directory entry
	if currentDir != "/" {
		row := createFileRow("..", true, true)
		fileList.Add(row)
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			row := createFileRow(entry.Name(), true, false)
			fileList.Add(row)
		}
	}

	// Add .paw files
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			row := createFileRow(entry.Name(), false, false)
			fileList.Add(row)
		}
	}

	fileList.ShowAll()
}

func createFileRow(name string, isDir bool, isParent bool) *gtk.ListBoxRow {
	row, _ := gtk.ListBoxRowNew()

	box, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	box.SetMarginStart(5)
	box.SetMarginEnd(5)
	box.SetMarginTop(2)
	box.SetMarginBottom(2)

	// Icon
	var iconName string
	if isParent {
		iconName = "go-up-symbolic"
	} else if isDir {
		iconName = "folder-symbolic"
	} else {
		iconName = "text-x-script-symbolic"
	}
	icon, _ := gtk.ImageNewFromIconName(iconName, gtk.ICON_SIZE_MENU)
	box.PackStart(icon, false, false, 0)

	// Name label
	label, _ := gtk.LabelNew(name)
	label.SetXAlign(0)
	label.SetHExpand(true)
	box.PackStart(label, true, true, 0)

	row.Add(box)
	row.SetName(name)

	return row
}

func onFileActivated(listbox *gtk.ListBox, row *gtk.ListBoxRow) {
	name, _ := row.GetName()
	handleFileSelection(name)
}

func onRunClicked() {
	row := fileList.GetSelectedRow()
	if row == nil {
		terminal.Feed("No file selected.\r\n")
		return
	}

	name, _ := row.GetName()
	handleFileSelection(name)
}

func handleFileSelection(name string) {
	fullPath := filepath.Join(currentDir, name)

	info, err := os.Stat(fullPath)
	if err != nil {
		terminal.Feed(fmt.Sprintf("Error: %v\r\n", err))
		return
	}

	if info.IsDir() {
		// Navigate to directory
		if name == ".." {
			currentDir = filepath.Dir(currentDir)
		} else {
			currentDir = fullPath
		}
		refreshFileList()
		updatePathLabel()
	} else {
		// Run the script
		runScript(fullPath)
	}
}

func onBrowseClicked() {
	dialog, _ := gtk.FileChooserDialogNewWith2Buttons(
		"Choose Directory",
		mainWindow,
		gtk.FILE_CHOOSER_ACTION_SELECT_FOLDER,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT,
	)

	dialog.SetCurrentFolder(currentDir)

	response := dialog.Run()
	if response == gtk.RESPONSE_ACCEPT {
		currentDir = dialog.GetFilename()
		refreshFileList()
		updatePathLabel()
	}
	dialog.Destroy()
}

func runScript(filePath string) {
	terminal.Feed(fmt.Sprintf("\r\n--- Running: %s ---\r\n\r\n", filepath.Base(filePath)))

	// Find the paw executable
	pawPath, err := exec.LookPath("paw")
	if err != nil {
		// Try relative to our executable
		if exe, err := os.Executable(); err == nil {
			pawPath = filepath.Join(filepath.Dir(exe), "paw")
		}
	}

	// Run script using VTE's async spawn capability
	cmd := terminal.NewCmd(pawPath, filePath)
	cmd.Dir = filepath.Dir(filePath)
	terminal.ExecAsync(cmd)
}
