package logging

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

func stderrSupportsColor() bool {
	fd := os.Stderr.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func NewStderrHandler(opts *tint.Options) slog.Handler {
	if opts == nil {
		return tint.NewHandler(os.Stderr, &tint.Options{NoColor: !stderrSupportsColor()})
	}

	resolved := tint.Options{
		AddSource:   opts.AddSource,
		Level:       opts.Level,
		ReplaceAttr: opts.ReplaceAttr,
		TimeFormat:  opts.TimeFormat,
		NoColor:     opts.NoColor || !stderrSupportsColor(),
	}

	return tint.NewHandler(os.Stderr, &resolved)
}
