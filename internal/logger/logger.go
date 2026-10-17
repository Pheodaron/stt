package logger

import (
	"log/slog"
	"os"
	"vks-stt-v2/internal/config"

	"github.com/lmittmann/tint"
)

func Init(config config.Config) {
	level := slog.LevelInfo
	if config.IsDev {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(getSlogHandler(config.IsDev, level)))
}

func getSlogHandler(isDev bool, level slog.Leveler) slog.Handler {
	if isDev {
		return tint.NewTextHandler(os.Stderr, &tint.Options{
			Level:      level,
			TimeFormat: "15:04:05",
		})
	}
	return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
}
