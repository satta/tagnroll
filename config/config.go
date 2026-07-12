package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	ProxmarkBinary string `yaml:"proxmark_binary"`
	Device         string `yaml:"device"`
	AESKeyGen      string `yaml:"aes_key_gen"`
	AESKeyCipher   string `yaml:"aes_key_cipher"`
}

const configFileName = ".tagnroll"

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		ProxmarkBinary: "", // Empty means auto-detect
		Device:         "/dev/ttyACM0",
	}
}

// Load loads the configuration from ~/.tagnroll
func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, configFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config if file doesn't exist
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults for empty values
	if config.Device == "" {
		config.Device = "/dev/ttyACM0"
	}

	return &config, nil
}

// Save saves the configuration to ~/.tagnroll
func (c *Config) Save() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, configFileName)
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
