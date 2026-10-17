package logger

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"vks-stt-v2/internal/config"
)

func Init(cfg config.Config) {
	level := slog.LevelInfo
	if cfg.IsDev {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(getSlogHandler(cfg.IsDev, level)))
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
