package linkoerr

import (
	"errors"
	"log/slog"
)

// errWithAttrs wraps an error and attaches structured slog attributes to it.
// The embedded error lets errWithAttrs implement the standard error interface
// without writing an explicit Error() method.
type errWithAttrs struct {
	error
	attrs []slog.Attr
}

// WithAttrs returns a new error that wraps err and carries structured
// attributes. If err is nil, WithAttrs returns nil.
func WithAttrs(err error, args ...any) error {
	if err == nil {
		return nil
	}
	return &errWithAttrs{
		error: err,
		attrs: argsToAttr(args),
	}
}

// argsToAttr turns a list of typed or untyped values into a slice of slog.Attr.
// args[i] is treated as a key if it is a string or an slog.Attr; otherwise, it
// is treated as a value with key "!BADKEY".
func argsToAttr(args []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(args))
	for i := 0; i < len(args); {
		switch key := args[i].(type) {
		case slog.Attr:
			attrs = append(attrs, key)
			i++
		case string:
			if i+1 >= len(args) {
				attrs = append(attrs, slog.String("!BADKEY", key))
				i++
			} else {
				attrs = append(attrs, slog.Any(key, args[i+1]))
				i += 2
			}
		default:
			attrs = append(attrs, slog.Any("!BADKEY", args[i]))
			i++
		}
	}
	return attrs
}

func (e *errWithAttrs) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.error
}

func (e *errWithAttrs) Attrs() []slog.Attr {
	if e == nil {
		return nil
	}
	return e.attrs
}

// attrError is implemented by errors that expose structured attributes.
type attrError interface {
	Attrs() []slog.Attr
}

// Attrs recursively extracts all logging attributes from an error chain. In the
// case of duplicate keys, the outermost value takes precedence.
func Attrs(err error) []slog.Attr {
	var attrs []slog.Attr
	for err != nil {
		if ae, ok := err.(attrError); ok {
			attrs = append(attrs, ae.Attrs()...)
		}
		err = errors.Unwrap(err)
	}
	return attrs
}
