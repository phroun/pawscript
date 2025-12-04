// pawgui-gtk - GTK3-based GUI for PawScript with custom terminal emulator
// Cross-platform: works on Linux, macOS, and Windows
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/phroun/pawscript/pkg/gtkterm"
)

const (
	appID   = "com.pawscript.pawgui-gtk"
	appName = "PawScript Launcher (GTK)"
)

// Global state
var (
	currentDir string
	mainWindow *gtk.ApplicationWindow
	fileList   *gtk.ListBox
	terminal   *gtkterm.Terminal
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
	mainWindow.SetDefaultSize(1100, 750)

	// Apply CSS for larger fonts throughout the UI (2x original size)
	cssProvider, _ := gtk.CssProviderNew()
	cssProvider.LoadFromData(`
		* {
			font-size: 20px;
		}
		button {
			padding: 12px 24px;
		}
		label {
			font-size: 20px;
		}
	`)
	screen := mainWindow.GetScreen()
	gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	// Create main vertical box for menu + content
	mainBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Create menu bar
	menuBar, _ := gtk.MenuBarNew()

	// File menu
	fileMenuItem, _ := gtk.MenuItemNewWithLabel("File")
	fileMenu, _ := gtk.MenuNew()
	fileMenuItem.SetSubmenu(fileMenu)

	// Quit menu item
	quitItem, _ := gtk.MenuItemNewWithLabel("Quit")
	quitItem.Connect("activate", func() {
		mainWindow.Close()
	})
	fileMenu.Append(quitItem)

	// Edit menu
	editMenuItem, _ := gtk.MenuItemNewWithLabel("Edit")
	editMenu, _ := gtk.MenuNew()
	editMenuItem.SetSubmenu(editMenu)

	// Copy menu item
	copyItem, _ := gtk.MenuItemNewWithLabel("Copy")
	copyItem.Connect("activate", func() {
		if terminal != nil {
			terminal.CopySelection()
		}
	})
	editMenu.Append(copyItem)

	// Select All menu item
	selectAllItem, _ := gtk.MenuItemNewWithLabel("Select All")
	selectAllItem.Connect("activate", func() {
		if terminal != nil {
			terminal.SelectAll()
		}
	})
	editMenu.Append(selectAllItem)

	// Clear menu item
	clearItem, _ := gtk.MenuItemNewWithLabel("Clear")
	clearItem.Connect("activate", func() {
		if terminal != nil {
			terminal.Clear()
		}
	})
	editMenu.Append(clearItem)

	menuBar.Append(fileMenuItem)
	menuBar.Append(editMenuItem)
	mainBox.PackStart(menuBar, false, false, 0)

	// Create main horizontal paned (split view)
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	paned.SetPosition(400)

	// Left panel: File browser
	leftPanel := createFileBrowser()
	paned.Pack1(leftPanel, false, false)

	// Right panel: Terminal (with left margin for spacing from divider)
	rightPanel := createTerminal()
	rightPanel.SetMarginStart(8) // 8 pixel spacer from divider
	paned.Pack2(rightPanel, true, false)

	mainBox.PackStart(paned, true, true, 0)
	mainWindow.Add(mainBox)

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()
	updatePathLabel()

	// Print welcome message
	terminal.Feed("PawScript Launcher (GTK3)\r\n")
	terminal.Feed("Cross-platform terminal emulator\r\n")
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

	// Create terminal with gtkterm package
	var err error
	terminal, err = gtkterm.New(gtkterm.Options{
		Cols:           100,
		Rows:           30,
		ScrollbackSize: 10000,
		FontFamily:     "Menlo",
		FontSize:       22, // 2x size for better readability
		Scheme: gtkterm.ColorScheme{
			Foreground: gtkterm.Color{R: 212, G: 212, B: 212},
			Background: gtkterm.Color{R: 30, G: 30, B: 30},
			Cursor:     gtkterm.Color{R: 255, G: 255, B: 255},
			Selection:  gtkterm.Color{R: 68, G: 68, B: 68},
			Palette:    gtkterm.ANSIColors,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create terminal: %v\n", err)
		// Create a placeholder label
		errLabel, _ := gtk.LabelNew(fmt.Sprintf("Terminal creation failed: %v", err))
		box.PackStart(errLabel, true, true, 0)
		return box
	}

	// Add terminal widget to box
	termWidget := terminal.Widget()
	termWidget.SetVExpand(true)
	termWidget.SetHExpand(true)
	box.PackStart(termWidget, true, true, 0)

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

	// Set working directory for the terminal
	terminal.SetWorkingDirectory(filepath.Dir(filePath))

	// Run the script
	if err := terminal.RunCommand(pawPath, filePath); err != nil {
		terminal.Feed(fmt.Sprintf("Error running script: %v\r\n", err))
	}
}
