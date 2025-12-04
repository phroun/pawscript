// pawgui-gtk - GTK4-based GUI for PawScript
// This is a proof of concept alternative to the Fyne-based GUI
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	pawscript "github.com/phroun/pawscript"
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
	terminal   *gtk.TextView
	termBuffer *gtk.TextBuffer
	termMutex  sync.Mutex
	app        *gtk.Application
)

func main() {
	app = gtk.NewApplication(appID, gio.ApplicationFlagsNone)
	app.ConnectActivate(func() { activate(app) })

	if code := app.Run(os.Args); code > 0 {
		os.Exit(code)
	}
}

func activate(app *gtk.Application) {
	// Create main window
	mainWindow = gtk.NewApplicationWindow(app)
	mainWindow.SetTitle(appName)
	mainWindow.SetDefaultSize(900, 600)

	// Create main horizontal paned (split view)
	paned := gtk.NewPaned(gtk.OrientationHorizontal)
	paned.SetPosition(300)

	// Left panel: File browser
	leftPanel := createFileBrowser()
	paned.SetStartChild(leftPanel)

	// Right panel: Terminal
	rightPanel := createTerminal()
	paned.SetEndChild(rightPanel)

	mainWindow.SetChild(paned)

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()

	// Print welcome message
	appendToTerminal("PawScript Launcher (GTK4)\n")
	appendToTerminal("Select a .paw file and click Run to execute.\n\n")

	mainWindow.Show()
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
	box := gtk.NewBox(gtk.OrientationVertical, 5)
	box.SetMarginStart(5)
	box.SetMarginEnd(5)
	box.SetMarginTop(5)
	box.SetMarginBottom(5)

	// Directory label
	dirLabel := gtk.NewLabel("")
	dirLabel.SetXAlign(0)
	dirLabel.SetMarkup("<b>Directory:</b>")
	box.Append(dirLabel)

	// Current path label
	pathLabel := gtk.NewLabel(currentDir)
	pathLabel.SetXAlign(0)
	pathLabel.SetWrap(true)
	pathLabel.SetSelectable(true)
	box.Append(pathLabel)

	// Scrolled window for file list
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	// File list
	fileList = gtk.NewListBox()
	fileList.SetSelectionMode(gtk.SelectionSingle)
	fileList.ConnectRowActivated(onFileActivated)
	scroll.SetChild(fileList)
	box.Append(scroll)

	// Button box
	buttonBox := gtk.NewBox(gtk.OrientationHorizontal, 5)

	runButton := gtk.NewButtonWithLabel("Run")
	runButton.ConnectClicked(onRunClicked)
	runButton.SetHExpand(true)
	buttonBox.Append(runButton)

	browseButton := gtk.NewButtonWithLabel("Browse...")
	browseButton.ConnectClicked(onBrowseClicked)
	browseButton.SetHExpand(true)
	buttonBox.Append(browseButton)

	box.Append(buttonBox)

	return box
}

func createTerminal() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 0)

	// Label
	label := gtk.NewLabel("")
	label.SetXAlign(0)
	label.SetMarkup("<b>Console Output:</b>")
	label.SetMarginStart(5)
	label.SetMarginTop(5)
	box.Append(label)

	// Scrolled window for terminal
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyAutomatic, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetHExpand(true)

	// Text view as terminal
	terminal = gtk.NewTextView()
	terminal.SetEditable(false)
	terminal.SetCursorVisible(false)
	terminal.SetWrapMode(gtk.WrapChar)
	terminal.SetMonospace(true)
	terminal.SetLeftMargin(5)
	terminal.SetRightMargin(5)
	terminal.SetTopMargin(5)
	terminal.SetBottomMargin(5)

	// Dark background for terminal look
	cssProvider := gtk.NewCSSProvider()
	cssProvider.LoadFromData(`
		textview {
			background-color: #1e1e1e;
			color: #d4d4d4;
		}
		textview text {
			background-color: #1e1e1e;
			color: #d4d4d4;
		}
	`)
	display := gdk.DisplayGetDefault()
	gtk.StyleContextAddProviderForDisplay(display, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	termBuffer = terminal.Buffer()
	scroll.SetChild(terminal)
	box.Append(scroll)

	return box
}

func refreshFileList() {
	// Clear existing items
	for {
		row := fileList.RowAtIndex(0)
		if row == nil {
			break
		}
		fileList.Remove(row)
	}

	// Read directory
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		appendToTerminal(fmt.Sprintf("Error reading directory: %v\n", err))
		return
	}

	// Add parent directory entry
	if currentDir != "/" {
		row := createFileRow("..", true, true)
		fileList.Append(row)
	}

	// Add directories first
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			row := createFileRow(entry.Name(), true, false)
			fileList.Append(row)
		}
	}

	// Add .paw files
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".paw") {
			row := createFileRow(entry.Name(), false, false)
			fileList.Append(row)
		}
	}
}

func createFileRow(name string, isDir bool, isParent bool) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()

	box := gtk.NewBox(gtk.OrientationHorizontal, 5)
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
	icon := gtk.NewImageFromIconName(iconName)
	box.Append(icon)

	// Name label
	label := gtk.NewLabel(name)
	label.SetXAlign(0)
	label.SetHExpand(true)
	box.Append(label)

	row.SetChild(box)

	// Store name as data
	row.SetName(name)

	return row
}

func onFileActivated(row *gtk.ListBoxRow) {
	name := row.Name()
	fullPath := filepath.Join(currentDir, name)

	info, err := os.Stat(fullPath)
	if err != nil {
		appendToTerminal(fmt.Sprintf("Error: %v\n", err))
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
	} else {
		// Run the script
		runScript(fullPath)
	}
}

func onRunClicked() {
	row := fileList.SelectedRow()
	if row == nil {
		appendToTerminal("No file selected.\n")
		return
	}

	name := row.Name()
	fullPath := filepath.Join(currentDir, name)

	info, err := os.Stat(fullPath)
	if err != nil {
		appendToTerminal(fmt.Sprintf("Error: %v\n", err))
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
	} else {
		// Run the script
		runScript(fullPath)
	}
}

func onBrowseClicked() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Choose Directory")
	dialog.SetInitialFolder(gio.NewFileForPath(currentDir))

	dialog.SelectFolder(context.Background(), &mainWindow.Window, func(result gio.AsyncResulter) {
		file, err := dialog.SelectFolderFinish(result)
		if err != nil {
			// User cancelled or error occurred
			return
		}
		if file != nil {
			currentDir = file.Path()
			refreshFileList()
		}
	})
}

func runScript(filePath string) {
	appendToTerminal(fmt.Sprintf("\n--- Running: %s ---\n\n", filepath.Base(filePath)))

	content, err := os.ReadFile(filePath)
	if err != nil {
		appendToTerminal(fmt.Sprintf("Error reading file: %v\n", err))
		return
	}

	scriptDir := filepath.Dir(filePath)

	// Create PawScript instance
	ps := pawscript.New(&pawscript.Config{
		Debug:                false,
		AllowMacros:          true,
		EnableSyntacticSugar: true,
		ShowErrorContext:     true,
		ContextLines:         2,
		ScriptDir:            scriptDir,
	})

	// Create custom IO channels that write to our terminal
	termWriter := &terminalWriter{}
	ioConfig := &pawscript.IOChannelConfig{
		Stdout: createOutputChannel(termWriter),
		Stderr: createOutputChannel(termWriter),
		Stdin:  createInputChannel(),
	}
	ps.RegisterStandardLibraryWithIO([]string{}, ioConfig)

	// Run in a goroutine to not block the UI
	go func() {
		result := ps.Execute(string(content), filePath, 0, 0)
		glib.IdleAdd(func() {
			if result == pawscript.BoolStatus(false) {
				appendToTerminal("\n--- Script execution failed ---\n")
			} else {
				appendToTerminal("\n--- Script completed ---\n")
			}
		})
	}()
}

// terminalWriter writes to the GTK terminal
type terminalWriter struct{}

func (w *terminalWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	glib.IdleAdd(func() {
		appendToTerminal(text)
	})
	return len(p), nil
}

func appendToTerminal(text string) {
	termMutex.Lock()
	defer termMutex.Unlock()

	if termBuffer == nil {
		return
	}

	endIter := termBuffer.EndIter()
	termBuffer.Insert(endIter, text)

	// Scroll to bottom
	mark := termBuffer.CreateMark("end", termBuffer.EndIter(), false)
	terminal.ScrollToMark(mark, 0, false, 0, 0)
}

func createOutputChannel(w io.Writer) *pawscript.StoredChannel {
	return &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		NativeSend: func(v interface{}) error {
			switch val := v.(type) {
			case []byte:
				w.Write(val)
			case string:
				w.Write([]byte(val))
			default:
				w.Write([]byte(fmt.Sprintf("%v", val)))
			}
			return nil
		},
		NativeRecv: func() (interface{}, error) {
			return nil, fmt.Errorf("cannot receive from output channel")
		},
	}
}

func createInputChannel() *pawscript.StoredChannel {
	// For now, just return a basic input channel
	// TODO: Implement proper input handling
	return &pawscript.StoredChannel{
		BufferSize:       0,
		Messages:         make([]pawscript.ChannelMessage, 0),
		Subscribers:      make(map[int]*pawscript.StoredChannel),
		NextSubscriberID: 1,
		IsClosed:         false,
		NativeSend: func(v interface{}) error {
			return fmt.Errorf("cannot send to input channel")
		},
		NativeRecv: func() (interface{}, error) {
			// TODO: Implement input from GTK
			return nil, fmt.Errorf("input not implemented yet")
		},
	}
}
