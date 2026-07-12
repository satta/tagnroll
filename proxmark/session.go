package proxmark

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	// Match all ANSI escape sequences: ESC followed by [ and any characters until a letter
	ansiEscape := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	return ansiEscape.ReplaceAllString(s, "")
}

// Session represents a persistent Proxmark3 session
type Session struct {
	cmd            *exec.Cmd
	pty            *os.File
	outputChan     chan string
	readerStopped  chan bool
	command        string
	device         string
	mu             sync.Mutex
	running        bool
	outputCallback func(string)
	initialOutput  []string
}

// NewSession creates a new persistent Proxmark3 session
func NewSession(command, device string) (*Session, error) {
	if command == "" {
		// Auto-detect command
		cmd := exec.Command("pm3", "--help")
		if cmd.Run() == nil {
			command = "pm3"
		} else {
			cmd = exec.Command("proxmark3", "--help")
			if cmd.Run() == nil {
				command = "proxmark3"
			} else {
				return nil, fmt.Errorf("proxmark3 client not found in PATH")
			}
		}
	}

	session := &Session{
		command: command,
		device:  device,
		running: false,
	}

	if err := session.start(); err != nil {
		return nil, err
	}

	return session, nil
}

// start starts the proxmark3 process
func (s *Session) start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	s.cmd = exec.Command(s.command, "-p", s.device)
	fmt.Fprintf(os.Stderr, "[proxmark session] Starting: %s\n", strings.Join(s.cmd.Args, " "))

	// Use PTY to make proxmark3 think it's running in a terminal
	ptyFile, err := pty.Start(s.cmd)
	if err != nil {
		return fmt.Errorf("failed to start PTY: %w", err)
	}

	s.pty = ptyFile
	s.outputChan = make(chan string, 100)
	s.readerStopped = make(chan bool)
	s.running = true

	// Start a single reader goroutine that reads all PTY output
	fmt.Fprintf(os.Stderr, "[proxmark session] Starting single reader goroutine...\n")
	go s.readerLoop()

	// Wait for prompt or timeout
	fmt.Fprintf(os.Stderr, "[proxmark session] Consuming initial output...\n")
	timeout := time.After(3 * time.Second)
	for {
		select {
		case line := <-s.outputChan:
			// Forward initial banner output to the VTE callback
			if s.outputCallback != nil {
				s.outputCallback(line)
			} else {
				s.initialOutput = append(s.initialOutput, line)
			}
			if strings.Contains(line, "pm3 -->") || strings.Contains(line, "proxmark3>") {
				fmt.Fprintf(os.Stderr, "[proxmark session] Initialization complete (prompt found)\n")
				return nil
			}
		case <-timeout:
			fmt.Fprintf(os.Stderr, "[proxmark session] Initialization complete (timeout)\n")
			return nil
		}
	}

}

// readerLoop is a single goroutine that reads all PTY output
func (s *Session) readerLoop() {
	buf := make([]byte, 1024)
	var lineBuffer strings.Builder
	for {
		n, err := s.pty.Read(buf)
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "closed") {
				fmt.Fprintf(os.Stderr, "[proxmark session] Reader error: %v\n", err)
			}
			break
		}
		for i := 0; i < n; i++ {
			c := buf[i]
			if c == '\n' {
				line := lineBuffer.String()
				//fmt.Fprintf(os.Stderr, "[proxmark session] Scan line: %s\n", line)
				s.outputChan <- line
				lineBuffer.Reset()
			} else if c != '\r' {
				lineBuffer.WriteByte(c)
			}
			// Check for prompt in buffer (even without newline)
			current := lineBuffer.String()
			if strings.Contains(current, "pm3 -->") || strings.Contains(current, "proxmark3>") {
				fmt.Fprintf(os.Stderr, "[proxmark session] Prompt detected in buffer: %s\n", current)
				s.outputChan <- current
				lineBuffer.Reset()
			}
		}
	}
	close(s.readerStopped)
}

// Stop stops the proxmark3 session
func (s *Session) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	if s.pty != nil {
		s.pty.Close()
	}

	if s.cmd != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}

	s.running = false
	return nil
}

// IsRunning returns whether the session is running
func (s *Session) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Restart restarts the session
func (s *Session) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.start()
}

// SetOutputCallback sets the callback for output
func (s *Session) SetOutputCallback(callback func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputCallback = callback
	for _, line := range s.initialOutput {
		callback(line)
	}
	s.initialOutput = nil
}

// SendCommand sends a command to the proxmark3 session and returns the output
func (s *Session) SendCommand(cmd string) (string, error) {
	fmt.Fprintf(os.Stderr, "[proxmark session] SendCommand: acquiring lock\n")
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[proxmark session] SendCommand: lock acquired\n")

	if !s.running {
		return "", fmt.Errorf("session is not running")
	}

	// Send command
	fmt.Fprintf(os.Stderr, "[proxmark session] Sending: %s\n", cmd)
	if _, err := fmt.Fprintln(s.pty, cmd); err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[proxmark session] Command sent, waiting for response...\n")

	// Read output from shared output channel until we see the prompt again
	var output strings.Builder
	timeout := time.After(30 * time.Second)
	lineCount := 0

	for {
		select {
		case <-timeout:
			fmt.Fprintf(os.Stderr, "[proxmark session] Timeout after %d lines\n", lineCount)
			return output.String(), fmt.Errorf("command timeout")
		case line := <-s.outputChan:
			lineCount++
			output.WriteString(line)
			output.WriteString("\n")
			//fmt.Fprintf(os.Stderr, "[proxmark session] Line %d: %s\n", lineCount, line)

			if s.outputCallback != nil {
				s.outputCallback(line)
			}

			// Check for prompt (proxmark3 prompt is "pm3 --> " or similar)
			// Accept formats: [usb] pm3 -->, pm3 -->, proxmark3>
			if strings.Contains(line, "pm3 -->") || strings.Contains(line, "proxmark3>") {
				// Only treat as prompt if it looks like a prompt (not a command echo)
				// Command echoes have format: [usb|script] pm3 --> command
				// Real prompts are just: [usb] pm3 --> or pm3 -->
				if !strings.Contains(line, "|") && len(line) < 50 {
					//fmt.Fprintf(os.Stderr, "[proxmark session] Prompt found after %d lines\n", lineCount)
					//fmt.Fprintf(os.Stderr, "[proxmark session] Output:\n%s\n", output.String())
					return output.String(), nil
				}
			}
		}
	}
}

// SetDevice changes the device (requires restart)
func (s *Session) SetDevice(device string) error {
	s.mu.Lock()
	s.device = device
	s.mu.Unlock()
	return s.Restart()
}

// GetDevice returns the current device
func (s *Session) GetDevice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.device
}

// ReadUID reads the UID from a tag on the Proxmark3
func (s *Session) ReadUID() (string, error) {
	fmt.Fprintf(os.Stderr, "[proxmark session] ReadUID: calling SendCommand\n")
	output, err := s.SendCommand("hf mf info")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxmark session] ReadUID: SendCommand failed: %v\n", err)
		return "", fmt.Errorf("failed to read UID: %w", err)
	}

	// Parse UID from output line by line for robustness
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Strip ANSI escape sequences for parsing
		line = stripANSI(line)
		idx := strings.Index(line, "UID")
		if idx >= 0 {
			// Extract substring after UID:
			colonIdx := strings.Index(line[idx:], ":")
			if colonIdx >= 0 {
				value := strings.TrimSpace(line[idx+colonIdx+1:])
				// Remove any non-hex characters and spaces
				var hexParts []string
				for _, field := range strings.Fields(value) {
					if matched, _ := regexp.MatchString(`^[0-9a-fA-F]{2}$`, field); matched {
						hexParts = append(hexParts, field)
					}
				}
				if len(hexParts) > 0 {
					uid := strings.ToUpper(strings.Join(hexParts, ""))
					fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Parsed UID=%s\n", uid)
					return uid, nil
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: No pattern matched, cannot parse UID\n")
	return "", fmt.Errorf("could not parse UID from output")
}

// ReadBlock reads a block from the tag
func (s *Session) ReadBlock(block int, key string, useKeyB bool) (string, error) {
	var cmd string
	if useKeyB {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Using KeyB for block %d\n", block)
		cmd = fmt.Sprintf("hf mf rdbl --blk %d -b -k %s", block, key)
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Using KeyA for block %d\n", block)
		cmd = fmt.Sprintf("hf mf rdbl --blk %d -k %s", block, key)
	}

	output, err := s.SendCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to read block %d: %w", block, err)
	}

	// Strip ANSI escape sequences for parsing
	output = stripANSI(output)

	// Parse block data from output
	reSpaced := regexp.MustCompile(`([0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2})`)
	matches := reSpaced.FindStringSubmatch(output)
	if len(matches) > 1 {
		blockData := strings.ReplaceAll(matches[1], " ", "")
		blockData = strings.ToUpper(blockData)
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Parsed block %d data (spaced format): %s\n", block, blockData)
		return blockData, nil
	}

	reCompact := regexp.MustCompile(`([0-9a-fA-F]{32})`)
	matches = reCompact.FindStringSubmatch(output)
	if len(matches) > 1 {
		blockData := strings.ToUpper(matches[1])
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Parsed block %d data (compact format): %s\n", block, blockData)
		return blockData, nil
	}

	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Could not parse block %d data from output\n", block)
	return "", fmt.Errorf("could not parse block data")
}

// WriteBlock writes a block to the tag
func (s *Session) WriteBlock(block int, key string, data string, useKeyB bool) error {
	var cmd string
	if useKeyB {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Using KeyB for block %d\n", block)
		cmd = fmt.Sprintf("hf mf wrbl --blk %d -b -k %s -d %s", block, key, data)
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Using KeyA for block %d\n", block)
		cmd = fmt.Sprintf("hf mf wrbl --blk %d -k %s -d %s", block, key, data)
	}

	_, err := s.SendCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to write block %d: %w", block, err)
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Successfully wrote block %d\n", block)
	}

	return nil
}

// DetectEncryptionStatus detects if a tag is encrypted
func (s *Session) DetectEncryptionStatus(uid string) (bool, error) {
	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Detecting encryption status for tag\n")
	_, err := s.ReadBlock(4, "FFFFFFFFFFFF", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Default key failed, tag appears to be encrypted\n")
		return true, nil
	}
	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Default key succeeded, tag appears to be unencrypted\n")
	return false, nil
}

// ReadTagWithKey reads tag data with a specific key
func (s *Session) ReadTagWithKey(key string) (string, string, string, error) {
	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Reading tag with generated key: %s\n", key)
	block1, err := s.ReadBlock(4, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 4: %w", err)
	}

	block2, err := s.ReadBlock(5, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 5: %w", err)
	}

	block3, err := s.ReadBlock(6, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 6: %w", err)
	}

	return block1, block2, block3, nil
}

// WriteTagWithKey writes encrypted data to a tag with a specific key
func (s *Session) WriteTagWithKey(key, block1, block2, block3 string, isEncrypted bool) error {
	if isEncrypted {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Tag is encrypted, using generated key with KeyB\n")
		if err := s.WriteBlock(4, key, block1, true); err != nil {
			return err
		}
		if err := s.WriteBlock(5, key, block2, true); err != nil {
			return err
		}
		if err := s.WriteBlock(6, key, block3, true); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Tag is unencrypted, setting security first then writing data\n")
		sectorTrailer := key + "FF078069" + key
		fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Setting sector trailer (block 7) with default key, new key: %s\n", key)
		if err := s.WriteBlock(7, "FFFFFFFFFFFF", sectorTrailer, false); err != nil {
			return err
		}

		if err := s.WriteBlock(4, key, block1, true); err != nil {
			return err
		}
		if err := s.WriteBlock(5, key, block2, true); err != nil {
			return err
		}
		if err := s.WriteBlock(6, key, block3, true); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "[proxmark session] Decision: Successfully wrote tag with key: %s\n", key)

	return nil
}
