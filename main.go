package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// Structure of the YAML configuration file.
type Config struct {
	ScanInterval int                          `yaml:"scan_interval"`
	Triggers     map[string]string            `yaml:"triggers"`
	Pills        map[string]map[string]string `yaml:"pills"`
}

// Create and configure the zap logger
var Logger *zap.SugaredLogger

func createLogger() *zap.SugaredLogger {
	const (
		red    = "\033[31m"
		green  = "\033[32m"
		yellow = "\033[33m"
		blue   = "\033[34m"
		reset  = "\033[0m"
	)
	// Custom encoder configuration for colored log output
	encoderConfig := zapcore.EncoderConfig{
		MessageKey: "message",
		LevelKey:   "level",
		TimeKey:    "time",
		EncodeLevel: func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			var color string
			switch l {
			case zapcore.DebugLevel:
				color = blue

			case zapcore.InfoLevel:
				color = green

			case zapcore.WarnLevel:
				color = yellow

			case zapcore.ErrorLevel:
				color = red

			case zapcore.FatalLevel:
				color = red

			case zapcore.PanicLevel:
				color = red

			default:
				color = reset
			}

			enc.AppendString(color + strings.ToUpper(l.String()) + reset)
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

func main() {
	Logger = createLogger()

	// Configuration loading
	configDir, err := os.UserConfigDir()
	if err != nil {
		Logger.Panicf("Couldn't define the config directory")
	}

	err = validateConfigSecurity(configDir + "/process_pillz.yaml")
	if err != nil {
		Logger.Fatal(err)
	}

	data, err := os.ReadFile(configDir + "/process_pillz.yaml")
	if err != nil {
		Logger.Panicf("Error while opening configuration file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		Logger.Panicf("Error while parsing configuration file: %v", err)
	}

	if err := validateConfig(&config); err != nil {
		Logger.Panicf("Configuration validation failed: %v", err)
	}

	// Initializing the manager and starting the loop
	pm := NewPillManager(config)
	defer pm.dbusConn.Close()
	defer pm.ticker.Stop()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		pm.eatPill(nil, "default") // Reset to default profile
		os.Exit(0)
	}()

	for range pm.ticker.C {
		pm.scanProcesses()
	}
}
