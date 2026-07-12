package proxmark

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const defaultDevice = "/dev/ttyACM0"

// Client represents a Proxmark3 client
type Client struct {
	command        string       // 'pm3' or 'proxmark3'
	device         string       // serial port, e.g. /dev/ttyACM0
	outputCallback func(string) // Callback for command output
}

// NewClient creates a new Proxmark3 client
func NewClient() (*Client, error) {
	// Try to detect which command is available
	cmd := exec.Command("pm3", "--help")
	fmt.Fprintf(os.Stderr, "[proxmark] %s\n", strings.Join(cmd.Args, " "))
	err := cmd.Run()
	if err == nil {
		return &Client{command: "pm3", device: defaultDevice}, nil
	}

	// Try alternative
	cmd = exec.Command("proxmark3", "--help")
	fmt.Fprintf(os.Stderr, "[proxmark] %s\n", strings.Join(cmd.Args, " "))
	err = cmd.Run()
	if err == nil {
		return &Client{command: "proxmark3", device: defaultDevice}, nil
	}

	return nil, fmt.Errorf("proxmark3 client not found in PATH")
}

// SetOutputCallback sets the callback function for command output
func (c *Client) SetOutputCallback(callback func(string)) {
	c.outputCallback = callback
}

// SetDevice sets the serial port device to use for Proxmark3 commands.
func (c *Client) SetDevice(device string) {
	c.device = device
}

// IsAvailable checks if Proxmark3 client is available
func IsAvailable() bool {
	client, err := NewClient()
	return err == nil && client != nil
}

// ReadUID reads the UID from a tag on the Proxmark3
func (c *Client) ReadUID() (string, error) {
	cmd := exec.Command(c.command, "-p", c.device, "-c", "hf mf info")
	fmt.Fprintf(os.Stderr, "[proxmark] %s\n", strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxmark] Command failed: %v\n", err)
		return "", fmt.Errorf("failed to read UID: %w", err)
	}

	outputStr := string(output)
	fmt.Fprintf(os.Stderr, "[proxmark] Raw output:\n%s\n", outputStr)
	if c.outputCallback != nil {
		c.outputCallback(outputStr)
	}

	// Parse UID from output using regex patterns
	patterns := []string{
		`UID\s*:\s*([0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2})`,
		`UID\s*:\s*([0-9a-fA-F]{14})`,
		`UID\s*:\s*([0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2})`,
		`UID\s*:\s*([0-9a-fA-F]{8})`,
		`UID\s*=\s*([0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2})`,
	}

	for i, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(outputStr)
		if len(matches) > 1 {
			uid := strings.ReplaceAll(matches[1], " ", "")
			uid = strings.ToUpper(uid)
			fmt.Fprintf(os.Stderr, "[proxmark] Decision: Pattern %d matched, UID=%s\n", i+1, uid)
			return uid, nil
		}
	}

	fmt.Fprintf(os.Stderr, "[proxmark] Decision: No pattern matched, cannot parse UID\n")
	return "", fmt.Errorf("could not parse UID from output")
}

// ExecuteCommand executes a Proxmark3 command
func (c *Client) ExecuteCommand(command string) (string, error) {
	cmd := exec.Command(c.command, "-p", c.device, "-c", command)
	fmt.Fprintf(os.Stderr, "[proxmark] %s\n", strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxmark] Command failed: %v\n", err)
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	outputStr := string(output)
	fmt.Fprintf(os.Stderr, "[proxmark] Output:\n%s\n", outputStr)
	if c.outputCallback != nil {
		c.outputCallback(outputStr)
	}

	return outputStr, nil
}

// WriteBlock writes a block to the tag
func (c *Client) WriteBlock(block int, key string, data string, useKeyB bool) error {
	var cmd string
	if useKeyB {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Using KeyB for block %d\n", block)
		cmd = fmt.Sprintf("hf mf wrbl --blk %d -b -k %s -d %s", block, key, data)
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Using KeyA for block %d\n", block)
		cmd = fmt.Sprintf("hf mf wrbl --blk %d -k %s -d %s", block, key, data)
	}

	_, err := c.ExecuteCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to write block %d: %w", block, err)
	}

	time.Sleep(100 * time.Millisecond) // Small delay between commands
	return nil
}

// ReadBlock reads a block from the tag
func (c *Client) ReadBlock(block int, key string, useKeyB bool) (string, error) {
	var cmd string
	if useKeyB {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Using KeyB for block %d\n", block)
		cmd = fmt.Sprintf("hf mf rdbl --blk %d -b -k %s", block, key)
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Using KeyA for block %d\n", block)
		cmd = fmt.Sprintf("hf mf rdbl --blk %d -k %s", block, key)
	}

	output, err := c.ExecuteCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to read block %d: %w", block, err)
	}

	// Parse block data from output
	// Try to match spaced format first (e.g., "FB 41 42 A3 ...")
	reSpaced := regexp.MustCompile(`([0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2}\s+[0-9a-fA-F]{2})`)
	matches := reSpaced.FindStringSubmatch(output)
	if len(matches) > 1 {
		blockData := strings.ReplaceAll(matches[1], " ", "")
		blockData = strings.ToUpper(blockData)
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Parsed block %d data (spaced format): %s\n", block, blockData)
		return blockData, nil
	}

	// Try to match compact format (e.g., "FB4142A3...")
	reCompact := regexp.MustCompile(`([0-9a-fA-F]{32})`)
	matches = reCompact.FindStringSubmatch(output)
	if len(matches) > 1 {
		blockData := strings.ToUpper(matches[1])
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Parsed block %d data (compact format): %s\n", block, blockData)
		return blockData, nil
	}

	fmt.Fprintf(os.Stderr, "[proxmark] Decision: Could not parse block %d data from output\n", block)
	return "", fmt.Errorf("could not parse block data")
}

// WriteTag writes encrypted data to a tag
func (c *Client) WriteTag(uid string, block1, block2, block3 string, isEncrypted bool) error {
	// Generate key from UID would be done by the caller
	// This function just handles the Proxmark3 commands
	// This is a placeholder - the actual key generation should happen in the main app
	return fmt.Errorf("key generation needed - use WriteTagWithKey instead")
}

// WriteTagWithKey writes encrypted data to a tag with a specific key
func (c *Client) WriteTagWithKey(key, block1, block2, block3 string, isEncrypted bool) error {
	if isEncrypted {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Tag is encrypted, using generated key with KeyB\n")
		// Tag is already encrypted, use the generated key with -b flag
		if err := c.WriteBlock(4, key, block1, true); err != nil {
			return err
		}
		if err := c.WriteBlock(5, key, block2, true); err != nil {
			return err
		}
		if err := c.WriteBlock(6, key, block3, true); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Tag is unencrypted, setting security first then writing data\n")
		// New tag: first set sector trailer with default key to establish the new key
		sectorTrailer := key + "FF078069" + key
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Setting sector trailer (block 7) with default key, new key: %s\n", key)
		if err := c.WriteBlock(7, "FFFFFFFFFFFF", sectorTrailer, false); err != nil {
			return err
		}

		// Now write data blocks using the new key with KeyB
		if err := c.WriteBlock(4, key, block1, true); err != nil {
			return err
		}
		if err := c.WriteBlock(5, key, block2, true); err != nil {
			return err
		}
		if err := c.WriteBlock(6, key, block3, true); err != nil {
			return err
		}
	}

	return nil
}

// DetectEncryptionStatus detects if a tag is encrypted by trying to read with default key first
func (c *Client) DetectEncryptionStatus(uid string) (bool, error) {
	fmt.Fprintf(os.Stderr, "[proxmark] Decision: Detecting encryption status for tag\n")

	// Try to read with default key first
	_, err := c.ReadBlock(4, "FFFFFFFFFFFF", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[proxmark] Decision: Default key failed, tag appears to be encrypted\n")
		return true, nil
	}

	fmt.Fprintf(os.Stderr, "[proxmark] Decision: Default key succeeded, tag appears to be unencrypted\n")
	return false, nil
}

// ReadTag reads and decrypts tag data
func (c *Client) ReadTag(uid string) (string, string, string, error) {
	fmt.Fprintf(os.Stderr, "[proxmark] Decision: Reading tag with default key (FFFFFFFFFFFF)\n")
	// Try to read with default key first
	block1, err := c.ReadBlock(4, "FFFFFFFFFFFF", false)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 4 with default key: %w", err)
	}

	block2, err := c.ReadBlock(5, "FFFFFFFFFFFF", false)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 5 with default key: %w", err)
	}

	block3, err := c.ReadBlock(6, "FFFFFFFFFFFF", false)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 6 with default key: %w", err)
	}

	return block1, block2, block3, nil
}

// ReadTagWithKey reads tag data with a specific key
func (c *Client) ReadTagWithKey(key string) (string, string, string, error) {
	fmt.Fprintf(os.Stderr, "[proxmark] Decision: Reading tag with generated key: %s\n", key)
	block1, err := c.ReadBlock(4, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 4: %w", err)
	}

	block2, err := c.ReadBlock(5, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 5: %w", err)
	}

	block3, err := c.ReadBlock(6, key, true)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read block 6: %w", err)
	}

	return block1, block2, block3, nil
}

// StartInteractive starts an interactive Proxmark3 session
func (c *Client) StartInteractive() (*exec.Cmd, io.WriteCloser, *bufio.Scanner, error) {
	cmd := exec.Command(c.command, "-p", c.device)
	fmt.Fprintf(os.Stderr, "[proxmark] %s\n", strings.Join(cmd.Args, " "))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to start command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	return cmd, stdin, scanner, nil
}
