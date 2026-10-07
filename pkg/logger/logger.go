package logger

// to be removed

// import (
// 	"context"
// 	"decision-manager/internal/app/constants"
// 	"fmt"
// 	"os"
// 	"path/filepath"
// 	"time"

// 	"github.com/spf13/viper"
// 	"go.uber.org/zap"
// 	"go.uber.org/zap/zapcore"
// 	"gopkg.in/natefinch/lumberjack.v2"
// )

// // GetLogger : Return SugaredLogger instance from library according to log level set in config
// // kept variadic fn as context is optional parameter
// func GetLogger(ctx ...context.Context) *zap.SugaredLogger {
// 	logLevel := LogLevel(viper.GetInt(constants.LoggerLevelKey))
// 	sugaredLogger := New(logLevel)
// 	if len(ctx) > 0 {
// 		return AddCorrelation(ctx[0], sugaredLogger)
// 	}
// 	return sugaredLogger
// }

// // LogLevel converts int to zap log level
// func LogLevel(level int) zapcore.Level {
// 	switch level {
// 	case 0:
// 		return zapcore.DebugLevel
// 	case 1:
// 		return zapcore.InfoLevel
// 	case 2:
// 		return zapcore.WarnLevel
// 	case 3:
// 		return zapcore.ErrorLevel
// 	default:
// 		return zapcore.InfoLevel
// 	}
// }

// // ISTTimeEncoder encodes time in IST timezone with YYYY-MM-DD HH:mm:ss format
// func ISTTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
// 	// Load IST timezone
// 	istLocation, err := time.LoadLocation("Asia/Kolkata")
// 	if err != nil {
// 		// Fallback to UTC+5:30 if timezone loading fails
// 		istLocation = time.FixedZone("IST", 5*60*60+30*60)
// 	}

// 	// Convert to IST and format as YYYY-MM-DD HH:mm:ss
// 	istTime := t.In(istLocation)
// 	enc.AppendString(istTime.Format("2006-01-02 15:04:05"))
// }

// // New creates a new logger with the specified log level and file output
// func New(level zapcore.Level) *zap.SugaredLogger {
// 	// Create logs directory if it doesn't exist
// 	logDir := viper.GetString("log.directory")
// 	if logDir == "" {
// 		logDir = "logs" // Default logs directory
// 	}

// 	if err := os.MkdirAll(logDir, 0755); err != nil {
// 		fmt.Printf("Failed to create log directory: %v\n", err)
// 	}

// 	// Generate log file name with timestamp
// 	logFileName := viper.GetString("log.filename")
// 	if logFileName == "" {
// 		timestamp := time.Now().Format("2006-01-02")
// 		logFileName = fmt.Sprintf("decision-manager-%s.log", timestamp)
// 	}

// 	logFilePath := filepath.Join(logDir, logFileName)

// 	// Configure log rotation
// 	logRotator := &lumberjack.Logger{
// 		Filename:   logFilePath,
// 		MaxSize:    viper.GetInt("log.maxsize"),    // Default: 100 MB
// 		MaxBackups: viper.GetInt("log.maxbackups"), // Default: 3
// 		MaxAge:     viper.GetInt("log.maxage"),     // Default: 30 days
// 		Compress:   viper.GetBool("log.compress"),  // Default: true
// 	}

// 	// Set defaults if not configured
// 	if logRotator.MaxSize == 0 {
// 		logRotator.MaxSize = 100
// 	}
// 	if logRotator.MaxBackups == 0 {
// 		logRotator.MaxBackups = 3
// 	}
// 	if logRotator.MaxAge == 0 {
// 		logRotator.MaxAge = 30
// 	}

// 	// Configure encoder with timestamps
// 	encoderConfig := zap.NewProductionEncoderConfig()
// 	encoderConfig.TimeKey = "timestamp"
// 	encoderConfig.EncodeTime = ISTTimeEncoder
// 	encoderConfig.EncodeDuration = zapcore.StringDurationEncoder
// 	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder

// 	// Create file writer core
// 	fileCore := zapcore.NewCore(
// 		zapcore.NewJSONEncoder(encoderConfig),
// 		zapcore.AddSync(logRotator),
// 		level,
// 	)

// 	// Create console writer core (optional)
// 	var cores []zapcore.Core
// 	cores = append(cores, fileCore)

// 	if viper.GetBool("log.console") {
// 		consoleCore := zapcore.NewCore(
// 			zapcore.NewJSONEncoder(encoderConfig),
// 			zapcore.AddSync(os.Stdout),
// 			level,
// 		)
// 		cores = append(cores, consoleCore)
// 	}

// 	// Combine cores
// 	core := zapcore.NewTee(cores...)

// 	// Build logger with caller information
// 	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

// 	return logger.Sugar()
// }

// // AddCorrelation adds correlation ID from context to logger
// func AddCorrelation(ctx context.Context, logger *zap.SugaredLogger) *zap.SugaredLogger {
// 	if correlationID, ok := ctx.Value(constants.CorrelationId).(string); ok && correlationID != "" {
// 		return logger.With("correlation_id", correlationID)
// 	}
// 	return logger
// }
