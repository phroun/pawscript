// pawgui-gtk - GTK-based GUI for PawScript
// This is a proof of concept alternative to the Fyne-based GUI
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	pawscript "github.com/phroun/pawscript"
)

const (
	appID   = "com.pawscript.pawgui-gtk"
	appName = "PawScript Launcher (GTK)"
)

// Global state
var (
	currentDir string
	mainWindow *gtk.Window
	fileList   *gtk.ListBox
	terminal   *gtk.TextView
	termBuffer *gtk.TextBuffer
	termMutex  sync.Mutex
)

func main() {
	gtk.Init(nil)

	// Create main window
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create window: %v\n", err)
		os.Exit(1)
	}
	mainWindow = win

	win.SetTitle(appName)
	win.SetDefaultSize(900, 600)
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	// Create main horizontal paned (split view)
	paned, err := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to create paned: %v\n", err)
		os.Exit(1)
	}
	paned.SetPosition(300)

	// Left panel: File browser
	leftPanel := createFileBrowser()
	paned.Pack1(leftPanel, false, false)

	// Right panel: Terminal
	rightPanel := createTerminal()
	paned.Pack2(rightPanel, true, false)

	win.Add(paned)
	win.ShowAll()

	// Load initial directory
	currentDir = getDefaultDir()
	refreshFileList()

	// Print welcome message
	appendToTerminal("PawScript Launcher (GTK)\n")
	appendToTerminal("Select a .paw file and click Run to execute.\n\n")

	gtk.Main()
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
	pathLabel, _ := gtk.LabelNew(currentDir)
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
	fileList.Connect("row-activated", onFileActivated)
	scroll.Add(fileList)
	box.PackStart(scroll, true, true, 0)

	// Button box
	buttonBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)

	runButton, _ := gtk.ButtonNewWithLabel("Run")
	runButton.Connect("clicked", onRunClicked)
	buttonBox.PackStart(runButton, true, true, 0)

	browseButton, _ := gtk.ButtonNewWithLabel("Browse...")
	browseButton.Connect("clicked", onBrowseClicked)
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

	// Scrolled window for terminal
	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroll.SetVExpand(true)
	scroll.SetHExpand(true)

	// Text view as terminal
	terminal, _ = gtk.TextViewNew()
	terminal.SetEditable(false)
	terminal.SetCursorVisible(false)
	terminal.SetWrapMode(gtk.WRAP_CHAR)
	terminal.SetMonospace(true)
	terminal.SetLeftMargin(5)
	terminal.SetRightMargin(5)
	terminal.SetTopMargin(5)
	terminal.SetBottomMargin(5)

	// Dark background for terminal look
	cssProvider, _ := gtk.CssProviderNew()
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
	screen, _ := gdk.ScreenGetDefault()
	gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

	termBuffer, _ = terminal.GetBuffer()
	scroll.Add(terminal)
	box.PackStart(scroll, true, true, 0)

	return box
}

func refreshFileList() {
	// Clear existing items
	children := fileList.GetChildren()
	children.Foreach(func(item interface{}) {
		if w, ok := item.(*gtk.Widget); ok {
			w.Destroy()
		}
	})

	// Read directory
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		appendToTerminal(fmt.Sprintf("Error reading directory: %v\n", err))
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
		iconName = "go-up"
	} else if isDir {
		iconName = "folder"
	} else {
		iconName = "text-x-script"
	}
	icon, _ := gtk.ImageNewFromIconName(iconName, gtk.ICON_SIZE_MENU)
	box.PackStart(icon, false, false, 0)

	// Name label
	label, _ := gtk.LabelNew(name)
	label.SetXAlign(0)
	box.PackStart(label, true, true, 0)

	row.Add(box)

	// Store metadata
	row.SetName(name)
	if isDir {
		row.SetTooltipText("Directory")
	} else {
		row.SetTooltipText("PawScript file")
	}

	return row
}

func onFileActivated(listBox *gtk.ListBox, row *gtk.ListBoxRow) {
	name := row.GetName()
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

func onRunClicked(button *gtk.Button) {
	row := fileList.GetSelectedRow()
	if row == nil {
		appendToTerminal("No file selected.\n")
		return
	}

	name := row.GetName()
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

func onBrowseClicked(button *gtk.Button) {
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
	}
	dialog.Destroy()
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
		if result == pawscript.BoolStatus(false) {
			glib.IdleAdd(func() {
				appendToTerminal("\n--- Script execution failed ---\n")
			})
		} else {
			glib.IdleAdd(func() {
				appendToTerminal("\n--- Script completed ---\n")
			})
		}
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

	endIter := termBuffer.GetEndIter()
	termBuffer.Insert(endIter, text)

	// Scroll to bottom
	endMark := termBuffer.CreateMark("end", termBuffer.GetEndIter(), false)
	terminal.ScrollToMark(endMark, 0, false, 0, 0)
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
