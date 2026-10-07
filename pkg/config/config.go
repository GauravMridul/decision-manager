package config

import (
	"decision-manager/internal/app/constants"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config represents application configuration
type Config struct {
	viper *viper.Viper
}

// New creates a new Config instance
func New() *Config {
	return &Config{
		viper: viper.New(),
	}
}

// NewConfig initializes and returns a Config
func NewConfig() *Config {
	cfg := New()

	env := os.Getenv(constants.EnvKey)
	if env == "" {
		env = constants.DevEnvironment
	}

	// Set up viper
	v := cfg.viper
	v.SetEnvPrefix("ESA")
	v.AutomaticEnv()

	if env == constants.DevEnvironment {
		// Load from local config file for development
		v.SetConfigName("config")
		v.SetConfigType("json")
		v.AddConfigPath("./config")

		// Use the dev config file path from constants if available
		if constants.DevConfigJsonFilePath != "" {
			dir := filepath.Dir(constants.DevConfigJsonFilePath)
			file := filepath.Base(constants.DevConfigJsonFilePath)
			ext := filepath.Ext(file)
			name := file[:len(file)-len(ext)]

			v.SetConfigName(name)
			v.AddConfigPath(dir)
		}

		// Load config file
		if err := v.ReadInConfig(); err != nil {
			// It's ok if config file is not found, but other errors should be handled
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				panic("Error reading config file: " + err.Error())
			}
		}
	} else {
		// For non-dev environments, we assume environment variables are used
		// You can add additional configuration sources here as needed
	}

	// Set common values
	v.Set(constants.Environment, env)

	return cfg
}

// GetString retrieves a string value from config
func (c *Config) GetString(key string) string {
	return c.viper.GetString(key)
}

// GetInt retrieves an integer value from config
func (c *Config) GetInt(key string) int {
	return c.viper.GetInt(key)
}

// GetBool retrieves a boolean value from config
func (c *Config) GetBool(key string) bool {
	return c.viper.GetBool(key)
}

// GetFloat64 retrieves a float64 value from config
func (c *Config) GetFloat64(key string) float64 {
	return c.viper.GetFloat64(key)
}

// GetViper returns the underlying viper instance
func (c *Config) GetViper() *viper.Viper {
	return c.viper
}

// SetViper sets values from config to global viper
func (c *Config) SetViper() {
	for _, key := range c.viper.AllKeys() {
		viper.Set(key, c.viper.Get(key))
	}
}
