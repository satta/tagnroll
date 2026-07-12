package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TagEntry represents a tag history entry
type TagEntry struct {
	UID          string    `json:"uid"`
	Batch        string    `json:"batch"`
	Date         string    `json:"date"`
	Supplier     string    `json:"supplier"`
	Material     string    `json:"material"`
	MaterialName string    `json:"material_name"`
	Color        string    `json:"color"`
	Length       string    `json:"length"`
	Serial       string    `json:"serial"`
	Timestamp    time.Time `json:"timestamp"`
}

// Manager manages tag history
type Manager struct {
	entries []TagEntry
	mu      sync.RWMutex
	file    string
}

// NewManager creates a new history manager
func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Use XDG Data Home directory for XDG compliance
	// XDG_DATA_HOME defaults to ~/.local/share
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(homeDir, ".local", "share")
	}

	historyDir := filepath.Join(dataHome, "tagnroll")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return nil, err
	}

	historyFile := filepath.Join(historyDir, "history.json")

	m := &Manager{
		file: historyFile,
	}

	if err := m.load(); err != nil {
		// If file doesn't exist, that's okay
		if !os.IsNotExist(err) {
			return nil, err
		}
	}

	return m, nil
}

// load loads history from file
func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.file)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &m.entries)
}

// save saves history to file (assumes lock is already held)
func (m *Manager) save() error {
	fmt.Fprintf(os.Stderr, "[history] save: starting save to %s\n", m.file)
	data, err := json.MarshalIndent(m.entries, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[history] save: marshal error: %v\n", err)
		return err
	}
	fmt.Fprintf(os.Stderr, "[history] save: writing %d bytes\n", len(data))
	err = os.WriteFile(m.file, data, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[history] save: write error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[history] save: write successful\n")
	}
	return err
}

// AddEntry adds a new tag entry to history, or updates an existing entry
// with the same material and color combination.
func (m *Manager) AddEntry(entry TagEntry) error {
	fmt.Fprintf(os.Stderr, "[history] AddEntry: acquiring lock\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[history] AddEntry: lock acquired\n")

	entry.Timestamp = time.Now()
	for i, existing := range m.entries {
		if existing.Material == entry.Material && existing.Color == entry.Color {
			m.entries[i] = entry
			fmt.Fprintf(os.Stderr, "[history] AddEntry: updated existing entry for material %s color %s\n", entry.Material, entry.Color)
			return m.save()
		}
	}

	m.entries = append(m.entries, entry)
	fmt.Fprintf(os.Stderr, "[history] AddEntry: entry added, calling save\n")

	return m.save()
}

// GetEntries returns all history entries
func (m *Manager) GetEntries() []TagEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid race conditions
	entries := make([]TagEntry, len(m.entries))
	copy(entries, m.entries)
	return entries
}

// FindByUID finds entries by UID
func (m *Manager) FindByUID(uid string) []TagEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []TagEntry
	for _, entry := range m.entries {
		if entry.UID == uid {
			results = append(results, entry)
		}
	}
	return results
}

// FindByMaterial finds entries by material code
func (m *Manager) FindByMaterial(material string) []TagEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []TagEntry
	for _, entry := range m.entries {
		if entry.Material == material {
			results = append(results, entry)
		}
	}
	return results
}

// FindByColor finds entries by color
func (m *Manager) FindByColor(color string) []TagEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []TagEntry
	for _, entry := range m.entries {
		if entry.Color == color {
			results = append(results, entry)
		}
	}
	return results
}

// DeleteEntry deletes an entry by index
func (m *Manager) DeleteEntry(index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if index < 0 || index >= len(m.entries) {
		return nil
	}

	m.entries = append(m.entries[:index], m.entries[index+1:]...)
	return m.save()
}

// DeleteByUID deletes the first entry matching the given UID
func (m *Manager) DeleteByUID(uid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, entry := range m.entries {
		if entry.UID == uid {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return m.save()
		}
	}
	return nil
}

// Clear clears all history
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = []TagEntry{}
	return m.save()
}
