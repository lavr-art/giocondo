package architecture_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go/parser"
	"go/token"
)

// Services, transports and workers of the panel and the agent take the logger with logger.FromCtx, so every record carries
// the correlation attributes the boundaries stored in the context.
func TestLogsThroughContextLogger(t *testing.T) {
	for _, root := range []string{"internal/service", "internal/transport", "internal/worker"} {
		err := filepath.WalkDir("../../"+root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}

			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")

			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}

			for _, im := range f.Imports {
				if dep, _ := strconv.Unquote(im.Path.Value); dep == "log/slog" {
					t.Errorf("%s imports log/slog; use logger.FromCtx from pkg/logger", rel)
				}
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
