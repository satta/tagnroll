package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"tagnroll/colorname"
	"tagnroll/config"
	"tagnroll/crypto"
	"tagnroll/history"
	"tagnroll/proxmark"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/napsy/go-vte"
)

type App struct {
	window          *gtk.Window
	pm3Client       *proxmark.Client
	pm3Session      *proxmark.Session
	historyManager  *history.Manager
	config          *config.Config
	materialCombo   *gtk.ComboBoxText
	colorEntry      *gtk.Entry
	colorNameLabel  *gtk.Label
	colorPreview    *gtk.DrawingArea
	weightCombo     *gtk.ComboBoxText
	batchEntry      *gtk.Entry
	dateCalendar    *gtk.Calendar
	supplierEntry   *gtk.Entry
	serialEntry     *gtk.Entry
	uidLabel        *gtk.Label
	outputText      *gtk.TextView
	terminalLog     *vte.Terminal
	historyList     *gtk.Box
	historyScrolled *gtk.ScrolledWindow
	progressBar     *gtk.ProgressBar
	currentUID      string
	currentTagData  crypto.TagData
}

func main() {
	gtk.Init(nil)

	app, err := createApp()
	if err != nil {
		log.Fatal(err)
	}

	app.window.ShowAll()
	gtk.Main()
}

func createApp() (*App, error) {
	app := &App{}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	app.config = cfg

	// Set AES keys from config (delayed validation until use)
	crypto.SetKeys(cfg.AESKeyGen, cfg.AESKeyCipher)

	// Create history manager
	histMgr, err := history.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create history manager: %w", err)
	}
	app.historyManager = histMgr

	// Create persistent Proxmark3 session
	if cfg.ProxmarkBinary != "" || proxmark.IsAvailable() {
		session, err := proxmark.NewSession(cfg.ProxmarkBinary, cfg.Device)
		if err != nil {
			log.Printf("Failed to create proxmark3 session: %v", err)
			app.appendTerminalLog(fmt.Sprintf("Failed to create proxmark3 session: %v", err))
		} else {
			app.pm3Session = session
			session.SetOutputCallback(app.appendTerminalLog)
			app.appendTerminalLog("Proxmark3 session created successfully")
			log.Printf("Proxmark3 session created successfully")
		}
	}

	// Fallback to old client if session fails
	if app.pm3Session == nil {
		log.Printf("Using single-shot proxmark3 client (session creation failed or not available)")
		app.appendTerminalLog("Using single-shot proxmark3 client (session creation failed or not available)")
		pm3Available := proxmark.IsAvailable()
		if pm3Available {
			client, err := proxmark.NewClient()
			if err == nil {
				app.pm3Client = client
				client.SetDevice(cfg.Device)
				client.SetOutputCallback(app.appendTerminalLog)
				app.appendTerminalLog("Proxmark3 client connected")
			} else {
				app.appendTerminalLog(fmt.Sprintf("Failed to create proxmark3 client: %v", err))
			}
		} else {
			app.appendTerminalLog("Proxmark3 not found")
		}
	}

	// Create main window
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return nil, err
	}
	app.window = win
	win.SetTitle("Tag'n'Roll")
	win.SetDefaultSize(800, 700)
	win.SetBorderWidth(5)
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	// Create main container
	mainBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, err
	}
	win.Add(mainBox)

	// Menu bar (add to mainBox to span full width and appear at top)
	menuBar, err := gtk.MenuBarNew()
	if err != nil {
		return nil, err
	}
	mainBox.PackStart(menuBar, false, false, 0)

	// Create horizontal container for main content and sidebar
	hbox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, err
	}
	mainBox.PackStart(hbox, true, true, 5)

	// Create left container for main form
	leftBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, err
	}
	leftBox.SetMarginEnd(5)
	hbox.PackStart(leftBox, true, true, 0)

	// File menu
	fileMenu, err := gtk.MenuNew()
	if err != nil {
		return nil, err
	}
	fileMenuItem, err := gtk.MenuItemNewWithLabel("File")
	if err != nil {
		return nil, err
	}
	fileMenuItem.SetSubmenu(fileMenu)
	menuBar.Append(fileMenuItem)

	// Settings menu item
	settingsMenuItem, err := gtk.MenuItemNewWithLabel("Settings")
	if err != nil {
		return nil, err
	}
	settingsMenuItem.Connect("activate", app.onShowSettings)
	fileMenu.Append(settingsMenuItem)

	// Quit menu item
	quitMenuItem, err := gtk.MenuItemNewWithLabel("Quit")
	if err != nil {
		return nil, err
	}
	quitMenuItem.Connect("activate", func() {
		gtk.MainQuit()
	})
	fileMenu.Append(quitMenuItem)

	// Status bar (inside left pane, just above terminal)
	statusBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	if err != nil {
		return nil, err
	}
	statusBox.SetMarginTop(5)
	statusBox.SetMarginBottom(5)

	statusLabel, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	if app.pm3Session != nil {
		statusLabel.SetMarkup("Proxmark3 Session Active")
	} else if app.pm3Client != nil {
		statusLabel.SetMarkup("Proxmark3 Connected (single-shot mode)")
	} else {
		statusLabel.SetMarkup("Proxmark3 Not Found")
	}
	statusBox.PackStart(statusLabel, false, false, 5)

	// Activity indicator
	progressBar, err := gtk.ProgressBarNew()
	if err != nil {
		return nil, err
	}
	app.progressBar = progressBar
	progressBar.Hide() // Hide initially
	statusBox.PackStart(progressBar, false, false, 5)

	// Main form frame
	formFrame, err := gtk.FrameNew("Tag Settings")
	if err != nil {
		return nil, err
	}
	leftBox.PackStart(formFrame, true, true, 5)

	formBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 5)
	if err != nil {
		return nil, err
	}
	formFrame.Add(formBox)

	// UID display (read-only)
	uidBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(uidBox, false, false, 5)

	uidLabel, err := gtk.LabelNew("UID:")
	if err != nil {
		return nil, err
	}
	uidBox.PackStart(uidLabel, false, false, 5)

	uidValueLabel, err := gtk.LabelNew("Not read")
	if err != nil {
		return nil, err
	}
	app.uidLabel = uidValueLabel
	uidBox.PackStart(uidValueLabel, false, false, 5)

	// Material
	materialBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(materialBox, false, false, 5)

	materialLabel, err := gtk.LabelNew("Material:")
	if err != nil {
		return nil, err
	}
	materialBox.PackStart(materialLabel, false, false, 5)

	materialCombo, err := gtk.ComboBoxTextNew()
	if err != nil {
		return nil, err
	}
	app.materialCombo = materialCombo

	// Add materials sorted alphabetically by name
	var materials []struct{ code, name string }
	for code, name := range crypto.MaterialCodes {
		materials = append(materials, struct{ code, name string }{code, name})
	}
	// Sort by name
	for i := 0; i < len(materials); i++ {
		for j := i + 1; j < len(materials); j++ {
			if materials[i].name > materials[j].name {
				materials[i], materials[j] = materials[j], materials[i]
			}
		}
	}
	// Add to combo box
	for _, m := range materials {
		materialCombo.Append(m.code, m.name)
	}
	materialCombo.SetActive(0)
	materialCombo.SetSizeRequest(200, -1) // Set reasonable width
	materialBox.PackStart(materialCombo, true, true, 5)

	// Color
	colorBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(colorBox, false, false, 5)

	colorLabel, err := gtk.LabelNew("Color:")
	if err != nil {
		return nil, err
	}
	colorBox.PackStart(colorLabel, false, false, 5)

	// Spool color icon (clickable for color picker)
	colorEventBox, err := gtk.EventBoxNew()
	if err != nil {
		return nil, err
	}
	spoolIcon, err := gtk.DrawingAreaNew()
	if err != nil {
		return nil, err
	}
	app.colorPreview = spoolIcon
	spoolIcon.SetSizeRequest(40, 40)
	spoolIcon.Connect("draw", app.drawSpoolIcon)
	colorEventBox.Add(spoolIcon)
	colorEventBox.SetVisibleWindow(true)
	colorEventBox.Connect("button-press-event", func() bool {
		app.onPickColor()
		return false
	})
	colorBox.PackStart(colorEventBox, false, false, 5)

	colorEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	app.colorEntry = colorEntry
	colorEntry.SetText("0FF0000")
	colorEntry.Connect("changed", func() {
		app.colorPreview.QueueDraw()
		app.updateColorNameLabel()
	})
	colorBox.PackStart(colorEntry, true, true, 5)

	colorNameLabel, err := gtk.LabelNew("Red")
	if err != nil {
		return nil, err
	}
	app.colorNameLabel = colorNameLabel
	colorNameLabel.SetXAlign(0)
	colorBox.PackStart(colorNameLabel, false, false, 5)

	// Weight
	weightBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(weightBox, false, false, 5)

	weightLabel, err := gtk.LabelNew("Weight:")
	if err != nil {
		return nil, err
	}
	weightBox.PackStart(weightLabel, false, false, 5)

	weightCombo, err := gtk.ComboBoxTextNew()
	if err != nil {
		return nil, err
	}
	app.weightCombo = weightCombo
	// Add weight options
	for code, name := range crypto.LengthCodes {
		weightCombo.Append(code, name)
	}
	weightCombo.SetActive(0)
	weightBox.PackStart(weightCombo, true, true, 5)

	// Batch
	batchBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(batchBox, false, false, 5)

	batchLabel, err := gtk.LabelNew("Batch:")
	if err != nil {
		return nil, err
	}
	batchBox.PackStart(batchLabel, false, false, 5)

	batchEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	app.batchEntry = batchEntry
	batchEntry.SetText("1A5")
	batchBox.PackStart(batchEntry, true, true, 5)

	// Date
	dateBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(dateBox, false, false, 5)

	dateLabel, err := gtk.LabelNew("Date:")
	if err != nil {
		return nil, err
	}
	dateBox.PackStart(dateLabel, false, false, 5)

	dateCalendar, err := gtk.CalendarNew()
	if err != nil {
		return nil, err
	}
	app.dateCalendar = dateCalendar
	// Default to 2024-01-20 (24120 in tag format)
	dateCalendar.SelectDay(20)
	dateCalendar.SelectMonth(0, 2024) // month is 0-indexed
	dateBox.PackStart(dateCalendar, false, false, 5)

	// Supplier
	supBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(supBox, false, false, 5)

	supLabel, err := gtk.LabelNew("Supplier:")
	if err != nil {
		return nil, err
	}
	supBox.PackStart(supLabel, false, false, 5)

	supplierEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	app.supplierEntry = supplierEntry
	supplierEntry.SetText("1B3D")
	supBox.PackStart(supplierEntry, true, true, 5)

	// Serial
	serialBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(serialBox, false, false, 5)

	serialLabel, err := gtk.LabelNew("Serial:")
	if err != nil {
		return nil, err
	}
	serialBox.PackStart(serialLabel, false, false, 5)

	serialEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	app.serialEntry = serialEntry
	serialBox.PackStart(serialEntry, true, true, 5)

	randomSerialBtn, err := gtk.ButtonNewWithLabel("🎲")
	if err != nil {
		return nil, err
	}
	randomSerialBtn.Connect("clicked", app.onRandomizeSerial)
	serialBox.PackStart(randomSerialBtn, false, false, 5)

	// Action buttons
	buttonBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	if err != nil {
		return nil, err
	}
	formBox.PackStart(buttonBox, false, false, 10)

	readBtn, err := gtk.ButtonNewWithLabel("Read Tag")
	if err != nil {
		return nil, err
	}
	readBtn.Connect("clicked", app.onReadTag)
	buttonBox.PackStart(readBtn, true, true, 5)

	writeBtn, err := gtk.ButtonNewWithLabel("Write Tag")
	if err != nil {
		return nil, err
	}
	writeBtn.Connect("clicked", app.onWriteTag)
	buttonBox.PackStart(writeBtn, true, true, 5)

	// Terminal log
	terminalFrame, err := gtk.FrameNew("Terminal Log")
	if err != nil {
		return nil, err
	}
	leftBox.PackStart(terminalFrame, false, false, 5)

	terminalScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return nil, err
	}
	terminalFrame.Add(terminalScrolled)

	terminalLog, err := vte.TerminalNew()
	if err != nil {
		return nil, err
	}
	app.terminalLog = terminalLog
	terminalScrolled.Add(terminalLog)

	// Set terminal to 24 rows (assuming ~20px per row)
	terminalLog.SetSizeRequest(-1, 24*20)
	terminalFrame.SetSizeRequest(-1, 24*20)

	// Pack terminal frame with fixed height
	leftBox.PackStart(terminalFrame, false, false, 5)

	// Create right sidebar for history
	rightBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, err
	}
	rightBox.SetMarginStart(5)
	rightBox.SetSizeRequest(270, -1)
	hbox.PackStart(rightBox, false, false, 0)

	// History frame
	historyFrame, err := gtk.FrameNew("History")
	if err != nil {
		return nil, err
	}
	rightBox.PackStart(historyFrame, true, true, 5)

	// Scrolled window for history list
	historyScrolled, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return nil, err
	}
	app.historyScrolled = historyScrolled
	historyFrame.Add(historyScrolled)

	// Box for history items (using buttons for reliable click handling)
	historyList, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	if err != nil {
		return nil, err
	}
	historyScrolled.Add(historyList)
	app.historyList = historyList

	// Load history entries on startup
	app.loadHistoryCombo()

	// Add status bar at the bottom of the main window
	mainBox.PackEnd(statusBox, false, false, 5)

	return app, nil
}

func (app *App) loadHistoryCombo() {
	fmt.Fprintf(os.Stderr, "[main] loadHistoryCombo: starting\n")

	// Clear existing buttons from box
	var children []gtk.IWidget
	childrenList := app.historyList.GetChildren()
	for ; childrenList != nil; childrenList = childrenList.Next() {
		if data := childrenList.Data(); data != nil {
			children = append(children, data.(gtk.IWidget))
		}
	}
	for _, child := range children {
		app.historyList.Remove(child)
	}

	// Load history entries
	entries := app.historyManager.GetEntries()
	fmt.Fprintf(os.Stderr, "[main] loadHistoryCombo: loading %d entries\n", len(entries))
	for i, entry := range entries {
		app.addHistoryItem(entry, i)
	}

	// Show all widgets
	app.historyList.ShowAll()
	fmt.Fprintf(os.Stderr, "[main] loadHistoryCombo: completed\n")
}

func (app *App) addHistoryItem(entry history.TagEntry, index int) {
	// Create button for the history item
	btn, err := gtk.ButtonNew()
	if err != nil {
		return
	}
	btn.SetRelief(gtk.RELIEF_NONE)

	// Create horizontal box for the item
	itemBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	if err != nil {
		return
	}
	btn.Add(itemBox)

	// Create spool color icon (left side)
	spoolIcon, err := gtk.DrawingAreaNew()
	if err != nil {
		return
	}
	spoolIcon.SetSizeRequest(40, 40)
	spoolIcon.Connect("draw", func(da *gtk.DrawingArea, cr *cairo.Context) {
		app.drawSpoolIconForColor(da, cr, entry.Color)
	})
	itemBox.PackStart(spoolIcon, false, false, 5)

	// Create vertical box for description (right side)
	descBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	if err != nil {
		return
	}
	itemBox.PackStart(descBox, true, true, 5)

	// Material label (first line)
	materialLabel, err := gtk.LabelNew(entry.MaterialName)
	if err != nil {
		return
	}
	materialLabel.SetXAlign(0) // Left align
	descBox.PackStart(materialLabel, false, false, 0)

	// Serial and date label (second line)
	humanDate := app.getHumanReadableDate(entry.Date)
	infoLabel, err := gtk.LabelNew(fmt.Sprintf("Serial: %s | Date: %s", entry.Serial, humanDate))
	if err != nil {
		return
	}
	infoLabel.SetXAlign(0) // Left align
	infoLabel.SetMarkup(fmt.Sprintf("<small>%s</small>", fmt.Sprintf("Serial: %s | Date: %s", entry.Serial, humanDate)))
	descBox.PackStart(infoLabel, false, false, 0)

	// Add button to history box
	app.historyList.PackStart(btn, false, false, 2)

	// Add click handler
	btn.Connect("clicked", func() {
		app.loadTagData(entry)
	})

	// Add right-click context menu
	btn.Connect("button-press-event", func(widget *gtk.Button, event *gdk.Event) bool {
		btnEvent := gdk.EventButtonNewFromEvent(event)
		if btnEvent == nil {
			return false
		}
		if btnEvent.Button() == 3 { // Right-click
			menu, err := gtk.MenuNew()
			if err != nil {
				return false
			}

			deleteItem, err := gtk.MenuItemNewWithLabel("Delete")
			if err != nil {
				return false
			}
			deleteItem.Connect("activate", func() {
				if err := app.historyManager.DeleteEntry(index); err != nil {
					fmt.Fprintf(os.Stderr, "[main] Failed to delete history entry: %v\n", err)
					return
				}
				app.loadHistoryCombo()
			})
			menu.Append(deleteItem)
			menu.ShowAll()
			menu.PopupAtPointer(event)
			return true
		}
		return false
	})
}

func (app *App) drawSpoolIconForColor(da *gtk.DrawingArea, cr *cairo.Context, colorHex string) {
	// Parse hex color (format: 0RRGGBB)
	var r, g, b float64
	if len(colorHex) == 7 && colorHex[0] == '0' {
		var ri, gi, bi int
		fmt.Sscanf(colorHex[1:], "%02X%02X%02X", &ri, &gi, &bi)
		r = float64(ri) / 255.0
		g = float64(gi) / 255.0
		b = float64(bi) / 255.0
	} else {
		// Default to gray if invalid
		r, g, b = 0.5, 0.5, 0.5
	}

	// Get widget dimensions
	width := da.GetAllocatedWidth()
	height := da.GetAllocatedHeight()
	centerX := float64(width) / 2
	centerY := float64(height) / 2

	// Draw outer circle (spool rim)
	cr.SetSourceRGB(r*0.7, g*0.7, b*0.7) // Darker shade for rim
	cr.Arc(centerX, centerY, float64(width)/2-2, 0, 2*math.Pi)
	cr.Fill()

	// Draw inner circle (spool hub)
	cr.SetSourceRGB(r, g, b) // Main color
	cr.Arc(centerX, centerY, float64(width)/3, 0, 2*math.Pi)
	cr.Fill()

	// Draw center hole
	cr.SetSourceRGB(0.9, 0.9, 0.9) // Light gray for hole
	cr.Arc(centerX, centerY, float64(width)/8, 0, 2*math.Pi)
	cr.Fill()
}

func (app *App) formatTagDate(year, month, day int) string {
	// Format as YYMDD (month is 1-indexed in time package)
	return fmt.Sprintf("%02d%d%02d", year%100, month, day)
}

func (app *App) parseTagDate(dateStr string) (year, month, day int, err error) {
	// Parse YYMDD format
	if len(dateStr) < 5 || len(dateStr) > 6 {
		return 0, 0, 0, fmt.Errorf("invalid date format: %s", dateStr)
	}

	// Parse year (2 digits)
	var yy, m, dd int
	if len(dateStr) == 5 {
		// Format: YYMDD (single digit month)
		fmt.Sscanf(dateStr, "%2d%1d%2d", &yy, &m, &dd)
	} else {
		// Format: YYMMDD (two digit month)
		fmt.Sscanf(dateStr, "%2d%2d%2d", &yy, &m, &dd)
	}

	// Convert 2-digit year to 4-digit year (assume 2000-2099)
	if yy < 50 {
		year = 2000 + yy
	} else {
		year = 1900 + yy
	}

	return year, m, dd, nil
}

func (app *App) getTagDate() string {
	year, month, day := app.dateCalendar.GetDate()
	// month from Calendar is 0-indexed, add 1
	return app.formatTagDate(int(year), int(month)+1, int(day))
}

func (app *App) setTagDate(dateStr string) {
	year, month, day, err := app.parseTagDate(dateStr)
	if err != nil {
		return
	}
	app.dateCalendar.SelectMonth(uint(month)-1, uint(year))
	app.dateCalendar.SelectDay(uint(day))
}

func (app *App) getHumanReadableDate(dateStr string) string {
	year, month, day, err := app.parseTagDate(dateStr)
	if err != nil {
		return dateStr
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

func (app *App) loadTagData(entry history.TagEntry) {
	app.materialCombo.SetActiveID(entry.Material)
	app.colorEntry.SetText(entry.Color)
	app.weightCombo.SetActiveID(entry.Length)
	app.batchEntry.SetText(entry.Batch)
	app.setTagDate(entry.Date)
	app.supplierEntry.SetText(entry.Supplier)
	app.serialEntry.SetText(entry.Serial)
	// Only display UID from history; do not use it for writing
	app.uidLabel.SetText(entry.UID)
}

func (app *App) startLoading() {
	glib.IdleAdd(func() {
		if app.progressBar == nil {
			return
		}
		app.progressBar.SetFraction(0.0)
		app.progressBar.Show()
		app.progressBar.Pulse()
	})

	glib.TimeoutAdd(100, func() bool {
		if app.progressBar == nil || !app.progressBar.GetVisible() {
			return false
		}
		app.progressBar.Pulse()
		return true
	})
}

func (app *App) stopLoading() {
	glib.IdleAdd(func() {
		if app.progressBar == nil {
			return
		}
		app.progressBar.SetFraction(0.0)
		app.progressBar.Hide()
	})
}

func (app *App) setColor(color string) {
	app.colorEntry.SetText(color)
	app.colorPreview.QueueDraw()
	app.updateColorNameLabel()
}

func (app *App) updateColorNameLabel() {
	colorHex, _ := app.colorEntry.GetText()
	name, err := colorname.ColorName(colorHex)
	if err != nil {
		app.colorNameLabel.SetText("")
		return
	}
	app.colorNameLabel.SetText(name)
}

func (app *App) onRandomizeSerial() {
	rand.Seed(time.Now().UnixNano())
	serial := fmt.Sprintf("%06X", rand.Intn(0xFFFFFF))
	app.serialEntry.SetText(serial)
}

func (app *App) onShowSettings() {
	dialog, err := gtk.DialogNew()
	if err != nil {
		app.appendOutput(fmt.Sprintf("Error creating settings dialog: %v", err))
		return
	}
	dialog.SetTitle("Settings")
	dialog.SetDefaultSize(400, 200)

	contentArea, err := dialog.GetContentArea()
	if err != nil {
		dialog.Destroy()
		return
	}

	vbox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 10)
	if err != nil {
		dialog.Destroy()
		return
	}
	contentArea.PackStart(vbox, true, true, 10)

	// Proxmark binary path
	binaryBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		dialog.Destroy()
		return
	}
	vbox.PackStart(binaryBox, false, false, 5)

	binaryLabel, err := gtk.LabelNew("Proxmark3 Binary:")
	if err != nil {
		dialog.Destroy()
		return
	}
	binaryBox.PackStart(binaryLabel, false, false, 5)

	binaryEntry, err := gtk.EntryNew()
	if err != nil {
		dialog.Destroy()
		return
	}
	binaryEntry.SetText(app.config.ProxmarkBinary)
	binaryBox.PackStart(binaryEntry, true, true, 5)

	// Device path
	deviceBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		dialog.Destroy()
		return
	}
	vbox.PackStart(deviceBox, false, false, 5)

	deviceLabel, err := gtk.LabelNew("Device:")
	if err != nil {
		dialog.Destroy()
		return
	}
	deviceBox.PackStart(deviceLabel, false, false, 5)

	deviceEntry, err := gtk.EntryNew()
	if err != nil {
		dialog.Destroy()
		return
	}
	deviceEntry.SetText(app.config.Device)
	deviceBox.PackStart(deviceEntry, true, true, 5)

	// AES Key Gen
	keyGenBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		dialog.Destroy()
		return
	}
	vbox.PackStart(keyGenBox, false, false, 5)

	keyGenLabel, err := gtk.LabelNew("AES Key Gen (16 chars):")
	if err != nil {
		dialog.Destroy()
		return
	}
	keyGenBox.PackStart(keyGenLabel, false, false, 5)

	keyGenEntry, err := gtk.EntryNew()
	if err != nil {
		dialog.Destroy()
		return
	}
	keyGenEntry.SetText(app.config.AESKeyGen)
	keyGenBox.PackStart(keyGenEntry, true, true, 5)

	// AES Key Cipher
	keyCipherBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		dialog.Destroy()
		return
	}
	vbox.PackStart(keyCipherBox, false, false, 5)

	keyCipherLabel, err := gtk.LabelNew("AES Key Cipher (16 chars):")
	if err != nil {
		dialog.Destroy()
		return
	}
	keyCipherBox.PackStart(keyCipherLabel, false, false, 5)

	keyCipherEntry, err := gtk.EntryNew()
	if err != nil {
		dialog.Destroy()
		return
	}
	keyCipherEntry.SetText(app.config.AESKeyCipher)
	keyCipherBox.PackStart(keyCipherEntry, true, true, 5)

	// Buttons
	buttonBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 5)
	if err != nil {
		dialog.Destroy()
		return
	}
	vbox.PackStart(buttonBox, false, false, 10)

	saveBtn, err := gtk.ButtonNewWithLabel("Save")
	if err != nil {
		dialog.Destroy()
		return
	}
	saveBtn.Connect("clicked", func() {
		binaryPath, _ := binaryEntry.GetText()
		devicePath, _ := deviceEntry.GetText()
		keyGen, _ := keyGenEntry.GetText()
		keyCipher, _ := keyCipherEntry.GetText()
		app.config.ProxmarkBinary = binaryPath
		app.config.Device = devicePath
		app.config.AESKeyGen = keyGen
		app.config.AESKeyCipher = keyCipher
		if err := app.config.Save(); err != nil {
			app.appendOutput(fmt.Sprintf("Error saving config: %v", err))
		} else {
			app.appendOutput("Settings saved.")
		}
		// Apply AES keys immediately
		if err := crypto.SetKeys(keyGen, keyCipher); err != nil {
			app.appendOutput(fmt.Sprintf("Error applying AES keys: %v", err))
		} else {
			app.appendOutput("AES keys applied.")
		}
		dialog.Destroy()
	})
	buttonBox.PackStart(saveBtn, true, true, 5)

	cancelBtn, err := gtk.ButtonNewWithLabel("Cancel")
	if err != nil {
		dialog.Destroy()
		return
	}
	cancelBtn.Connect("clicked", func() {
		dialog.Destroy()
	})
	buttonBox.PackStart(cancelBtn, true, true, 5)

	dialog.ShowAll()
}

func (app *App) drawSpoolIcon(da *gtk.DrawingArea, cr *cairo.Context) {
	// Get current color from entry
	colorHex, _ := app.colorEntry.GetText()

	// Parse hex color (format: 0RRGGBB)
	var r, g, b float64
	if len(colorHex) == 7 && colorHex[0] == '0' {
		var ri, gi, bi int
		fmt.Sscanf(colorHex[1:], "%02X%02X%02X", &ri, &gi, &bi)
		r = float64(ri) / 255.0
		g = float64(gi) / 255.0
		b = float64(bi) / 255.0
	} else {
		// Default to gray if invalid
		r, g, b = 0.5, 0.5, 0.5
	}

	// Get widget dimensions
	width := da.GetAllocatedWidth()
	height := da.GetAllocatedHeight()
	centerX := float64(width) / 2
	centerY := float64(height) / 2

	// Draw outer circle (spool rim)
	cr.SetSourceRGB(r*0.7, g*0.7, b*0.7) // Darker shade for rim
	cr.Arc(centerX, centerY, float64(width)/2-2, 0, 2*math.Pi)
	cr.Fill()

	// Draw inner circle (spool hub)
	cr.SetSourceRGB(r, g, b) // Main color
	cr.Arc(centerX, centerY, float64(width)/3, 0, 2*math.Pi)
	cr.Fill()

	// Draw center hole
	cr.SetSourceRGB(0.9, 0.9, 0.9) // Light gray for hole
	cr.Arc(centerX, centerY, float64(width)/8, 0, 2*math.Pi)
	cr.Fill()
}

func (app *App) onPickColor() {
	colorChooser, err := gtk.ColorChooserDialogNew("Choose Color", app.window)
	if err != nil {
		app.appendOutput(fmt.Sprintf("Error creating color chooser: %v", err))
		return
	}

	response := colorChooser.Run()
	if response == gtk.RESPONSE_OK {
		color := colorChooser.GetRGBA()

		// Convert RGBA to hex format (0RRGGBB)
		r := uint8(color.GetRed() * 255)
		g := uint8(color.GetGreen() * 255)
		b := uint8(color.GetBlue() * 255)
		hexColor := fmt.Sprintf("0%02X%02X%02X", r, g, b)
		app.setColor(hexColor)
	}

	colorChooser.Destroy()
}

func (app *App) appendOutput(text string) {
	if app.outputText == nil {
		return
	}
	glib.IdleAdd(func() {
		buffer, _ := app.outputText.GetBuffer()
		iter := buffer.GetEndIter()
		buffer.Insert(iter, text+"\n")
	})
}

func (app *App) appendTerminalLog(text string) {
	glib.IdleAdd(func() {
		if text == "" || app.terminalLog == nil {
			return
		}
		// Do not add newline after prompt lines
		if strings.Contains(text, "pm3 -->") || strings.Contains(text, "proxmark3>") {
			app.terminalLog.Feed(text)
		} else {
			app.terminalLog.Feed(text + "\r\n")
		}
	})
}

func (app *App) onWriteTag() {
	if app.pm3Session == nil && app.pm3Client == nil {
		app.appendOutput("Error: Proxmark3 not connected")
		return
	}

	// Get values from UI
	materialCode := app.materialCombo.GetActiveID()
	color, _ := app.colorEntry.GetText()
	weightCode := app.weightCombo.GetActiveID()
	batch, _ := app.batchEntry.GetText()
	date := app.getTagDate()
	supplier, _ := app.supplierEntry.GetText()
	serial, _ := app.serialEntry.GetText()
	app.startLoading()

	go func() {
		// Get UID from the actual tag on the reader
		glib.IdleAdd(func() {
			app.appendOutput("Reading UID from tag...")
		})
		var uid string
		var err error
		if app.pm3Session != nil {
			uid, err = app.pm3Session.ReadUID()
		} else {
			uid, err = app.pm3Client.ReadUID()
		}
		if err != nil {
			glib.IdleAdd(func() {
				app.appendOutput(fmt.Sprintf("Error reading UID: %v", err))
				app.stopLoading()
			})
			return
		}
		app.currentUID = uid
		glib.IdleAdd(func() {
			app.uidLabel.SetText(uid)
			app.appendOutput(fmt.Sprintf("UID: %s", uid))
		})
		// Detect encryption status
		var isEncrypted bool
		if app.pm3Session != nil {
			isEncrypted, err = app.pm3Session.DetectEncryptionStatus(uid)
		} else {
			isEncrypted, err = app.pm3Client.DetectEncryptionStatus(uid)
		}
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error detecting encryption status: %v", err))
				app.stopLoading()
				return
			}
			if isEncrypted {
				app.appendOutput("Tag is encrypted")
			} else {
				app.appendOutput("Tag is unencrypted")
			}
		})
		if err != nil {
			return
		}

		// Build tag data
		tagData := crypto.TagData{
			Batch:    batch,
			Date:     date,
			Supplier: supplier,
			Material: materialCode,
			Color:    color,
			Length:   weightCode,
			Serial:   serial,
			Reserve:  "00000000000000",
		}

		asciiData, err := crypto.BuildTagData(tagData)
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error building tag data: %v", err))
				app.stopLoading()
				return
			}
			app.appendOutput(fmt.Sprintf("Tag Data: %s", asciiData))
		})
		if err != nil {
			return
		}

		// Encrypt data
		block1, block2, block3, err := crypto.EncryptTagData(asciiData)
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error encrypting: %v", err))
				app.stopLoading()
				return
			}
			app.appendOutput(fmt.Sprintf("Block 4: %s", block1))
			app.appendOutput(fmt.Sprintf("Block 5: %s", block2))
			app.appendOutput(fmt.Sprintf("Block 6: %s", block3))
		})
		if err != nil {
			return
		}

		// Generate key
		key, err := crypto.GenerateKeyFromUID(uid)
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error generating key: %v", err))
				app.stopLoading()
				return
			}
			app.appendOutput(fmt.Sprintf("Key: %s", key))
		})
		if err != nil {
			return
		}

		// Write to tag
		if app.pm3Session != nil {
			err = app.pm3Session.WriteTagWithKey(key, block1, block2, block3, isEncrypted)
		} else {
			err = app.pm3Client.WriteTagWithKey(key, block1, block2, block3, isEncrypted)
		}
		fmt.Fprintf(os.Stderr, "[main] Decision: WriteTagWithKey result: %v\n", err)
		if err != nil {
			glib.IdleAdd(func() {
				app.appendOutput(fmt.Sprintf("Error writing to tag: %v", err))
				app.stopLoading()
			})
			return
		}

		glib.IdleAdd(func() {
			app.appendOutput("Successfully wrote to tag!")
		})

		// Add to history
		entry := history.TagEntry{
			UID:          uid,
			Batch:        batch,
			Date:         date,
			Supplier:     supplier,
			Material:     materialCode,
			MaterialName: crypto.GetMaterialName(materialCode),
			Color:        color,
			Length:       weightCode,
			Serial:       serial,
		}

		fmt.Fprintf(os.Stderr, "[main] Decision: Adding entry to history: %v\n", entry)
		if err := app.historyManager.AddEntry(entry); err != nil {
			glib.IdleAdd(func() {
				app.appendOutput(fmt.Sprintf("Warning: Failed to save to history: %v", err))
			})
		}
		fmt.Fprintf(os.Stderr, "[main] Decision: History add result: %v\n", err)

		// Update UI on main thread
		glib.IdleAdd(func() {
			app.loadHistoryCombo()
			app.stopLoading()
		})
	}()
}

func (app *App) onReadTag() {
	log.Printf("onReadTag called")
	if app.pm3Session == nil && app.pm3Client == nil {
		log.Printf("No proxmark connection available")
		app.appendOutput("Error: Proxmark3 not connected")
		return
	}

	log.Printf("Using session: %v, Using client: %v", app.pm3Session != nil, app.pm3Client != nil)
	app.appendOutput("Reading tag...")
	app.startLoading()

	go func() {
		log.Printf("Goroutine started")
		// First read UID
		var uid string
		var err error
		if app.pm3Session != nil {
			log.Printf("Reading UID from session")
			uid, err = app.pm3Session.ReadUID()
		} else {
			log.Printf("Reading UID from client")
			uid, err = app.pm3Client.ReadUID()
		}
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error reading UID: %v", err))
				app.stopLoading()
				return
			}

			app.currentUID = uid
			app.uidLabel.SetText(uid)
			app.appendOutput(fmt.Sprintf("UID: %s", uid))
		})
		if err != nil {
			return
		}

		// Generate key
		key, err := crypto.GenerateKeyFromUID(uid)
		if err != nil {
			glib.IdleAdd(func() {
				app.appendOutput(fmt.Sprintf("Error generating key: %v", err))
				app.stopLoading()
			})
			return
		}

		// Read blocks with generated key
		var block1, block2, block3 string
		if app.pm3Session != nil {
			block1, block2, block3, err = app.pm3Session.ReadTagWithKey(key)
		} else {
			block1, block2, block3, err = app.pm3Client.ReadTagWithKey(key)
		}
		glib.IdleAdd(func() {
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error reading tag: %v", err))
				app.stopLoading()
				return
			}

			app.appendOutput(fmt.Sprintf("Block 4: %s", block1))
			app.appendOutput(fmt.Sprintf("Block 5: %s", block2))
			app.appendOutput(fmt.Sprintf("Block 6: %s", block3))

			// Decrypt
			asciiData, _, err := crypto.DecryptTagData(block1, block2, block3)
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error decrypting: %v", err))
				app.stopLoading()
				return
			}

			app.appendOutput(fmt.Sprintf("Decrypted: %s", asciiData))

			// Parse
			tagData, err := crypto.ParseTagData(asciiData)
			if err != nil {
				app.appendOutput(fmt.Sprintf("Error parsing: %v", err))
				app.stopLoading()
				return
			}

			app.currentTagData = tagData
			app.appendOutput(fmt.Sprintf("Material: %s (%s)", crypto.GetMaterialName(tagData.Material), tagData.Material))
			app.appendOutput(fmt.Sprintf("Color: #%s", tagData.Color[1:]))
			app.appendOutput(fmt.Sprintf("Weight: %s", crypto.GetWeight(tagData.Length)))
			app.appendOutput(fmt.Sprintf("Serial: %s", tagData.Serial))

			// Load into form
			app.materialCombo.SetActiveID(tagData.Material)
			app.colorEntry.SetText(tagData.Color)
			app.weightCombo.SetActiveID(tagData.Length)
			app.batchEntry.SetText(tagData.Batch)
			app.setTagDate(tagData.Date)
			app.supplierEntry.SetText(tagData.Supplier)
			app.serialEntry.SetText(tagData.Serial)
			app.stopLoading()
		})
	}()
}
