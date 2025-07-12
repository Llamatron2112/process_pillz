package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// Version information - set by build flags
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Structure of the YAML configuration file.
type Config struct {
	ScanInterval int                          `yaml:"scan_interval"`
	Triggers     map[string]string            `yaml:"triggers"`
	Pills        map[string]map[string]string `yaml:"pills"`
	Blacklist    []string                     `yaml:"blacklist"`
}

// Create and configure the zap logger
var Logger *zap.SugaredLogger

func createLogger() *zap.SugaredLogger {
	// Custom encoder configuration for colored log output
	encoderConfig := zapcore.EncoderConfig{
		MessageKey: "message",
		LevelKey:   "level",
		EncodeLevel: func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			// Use the built-in color encoder but trim to 3 letters
			switch l {
			case zapcore.DebugLevel:
				enc.AppendString("\033[34mDEB\033[0m") // Blue
			case zapcore.InfoLevel:
				enc.AppendString("\033[32mINF\033[0m") // Green
			case zapcore.WarnLevel:
				enc.AppendString("\033[33mWAR\033[0m") // Yellow
			case zapcore.ErrorLevel:
				enc.AppendString("\033[31mERR\033[0m") // Red
			case zapcore.FatalLevel:
				enc.AppendString("\033[31mFAT\033[0m") // Red
			case zapcore.PanicLevel:
				enc.AppendString("\033[31mPAN\033[0m") // Red
			default:
				enc.AppendString(strings.ToUpper(l.String())[:3])
			}
		},
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}

	// Create a new logger with the custom encoder configuration
	zapConfig := zap.Config{
		Level:            zap.NewAtomicLevelAt(zap.DebugLevel),
		Development:      true,
		Sampling:         nil,
		Encoding:         "console",
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := zapConfig.Build()
	if err != nil {
		panic(err)
	}

	return logger.Sugar()
}

// Basic validation of the configuration
func validateConfig(config *Config) error {
	if config.ScanInterval <= 0 {
		return fmt.Errorf("scan_interval must be greater than 0, got %d", config.ScanInterval)
	}

	if len(config.Triggers) == 0 {
		return fmt.Errorf("triggers section cannot be empty")
	}

	if len(config.Pills) == 0 {
		return fmt.Errorf("pills section cannot be empty")
	}

	for triggerName, processName := range config.Triggers {
		if strings.TrimSpace(triggerName) == "" {
			return fmt.Errorf("trigger name cannot be empty")
		}
		if strings.TrimSpace(processName) == "" {
			return fmt.Errorf("process name for trigger '%s' cannot be empty", triggerName)
		}
	}

	for pillName, pillConfig := range config.Pills {
		if strings.TrimSpace(pillName) == "" {
			return fmt.Errorf("pill name cannot be empty")
		}
		if len(pillConfig) == 0 {
			return fmt.Errorf("pill configuration for '%s' cannot be empty", pillName)
		}
		for key, value := range pillConfig {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("configuration key in pill '%s' cannot be empty", pillName)
			}
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("configuration value for key '%s' in pill '%s' cannot be empty", key, pillName)
			}
		}
	}

	return nil
}

// Checks permissions on the config file, for security
func validateConfigSecurity(configPath string) error {
	info, err := os.Stat(configPath)
	if err != nil {
		return err
	}

	// Check file permissions (should not be world-writable)
	if info.Mode().Perm()&0002 != 0 {
		return fmt.Errorf("config file is world-writable")
	}

	// Check ownership
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if stat.Uid != uint32(os.Getuid()) {
			return fmt.Errorf("config file not owned by current user")
		}
	}

	return nil
}

// Find configuration file by searching in multiple locations
func findConfigFile() (string, error) {
	var searchPaths []string

	// Get user config directory
	if configDir, err := os.UserConfigDir(); err == nil {
		searchPaths = append(searchPaths,
			filepath.Join(configDir, "process_pillz.yaml"),
			filepath.Join(configDir, "process_pillz", "config.yaml"),
		)
	}

	// Add system-wide and example paths
	searchPaths = append(searchPaths,
		"/etc/process_pillz/config.yaml",
		"/usr/share/process_pillz/process_pillz.yaml.example",
	)

	var triedPaths []string
	for _, path := range searchPaths {
		triedPaths = append(triedPaths, path)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no configuration file found. Searched paths:\n  %s", strings.Join(triedPaths, "\n  "))
}

// Load and validate configuration from file
func loadConfig() (*Config, string, error) {
	configPath, err := findConfigFile()
	if err != nil {
		return nil, "", err
	}

	// Only validate security for user-owned files (not system examples)
	if !strings.HasPrefix(configPath, "/usr/share/") {
		if err := validateConfigSecurity(configPath); err != nil {
			return nil, "", fmt.Errorf("config security validation failed for %s: %v", configPath, err)
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("error reading config file %s: %v", configPath, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, "", fmt.Errorf("error parsing config file %s: %v", configPath, err)
	}

	if err := validateConfig(&config); err != nil {
		return nil, "", fmt.Errorf("config validation failed for %s: %v", configPath, err)
	}

	return &config, configPath, nil
}

// watchConfigFile watches the config file and sends a signal when it changes
func watchConfigFile(configPath string, restartChan chan struct{}) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		Logger.Errorf("Failed to create config watcher: %v", err)
		return
	}
	defer watcher.Close()

	// Watch the config file
	err = watcher.Add(configPath)
	if err != nil {
		Logger.Errorf("Failed to watch config file: %v", err)
		return
	}

	Logger.Infof("Watching config file for changes: %s", configPath)

	// Debounce timer to avoid multiple rapid restarts
	var debounceTimer *time.Timer
	const debounceDelay = 1 * time.Second

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				Logger.Infof("Config file changed: %s", event.Name)

				// Reset debounce timer
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDelay, func() {
					select {
					case restartChan <- struct{}{}:
					default:
						// Channel is full, restart already pending
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			Logger.Errorf("Config watcher error: %v", err)
		}
	}
}

func main() {
	Logger = createLogger()

	Logger.Infof("Process Pillz %s (commit %s, built %s)", Version, GitCommit, BuildTime)

	// Configuration loading with multi-path support
	config, configPath, err := loadConfig()
	if err != nil {
		Logger.Fatalf("Configuration error: %v", err)
	}

	Logger.Infof("Using configuration file: %s", configPath)

	// Create restart channel for config watcher
	restartChan := make(chan struct{}, 1)

	// Initializing the manager and starting the loop
	pm := NewPillManager(*config)

	pm.connectToDbus()

	defer pm.dbusConn.Close()
	defer pm.ticker.Stop()

	// Start config file watcher in a goroutine
	go watchConfigFile(configPath, restartChan)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigChan:
			Logger.Info("Shutting down...")
			pm.eatPill(nil, "default") // Reset to default profile
			pm.Close()
			os.Exit(0)

		case <-restartChan:
			Logger.Info("Config file changed, restarting...")
			pm.eatPill(nil, "default") // Reset to default profile
			pm.Close()
			os.Exit(42) // Special exit code to indicate restart needed

		case <-pm.ticker.C:
			pm.scanProcesses()
		}
	}
}
