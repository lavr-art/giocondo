package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const usage = "usage: bench prepare-packages ARCH MODE | unpack-packages | archive [ROOT] | control | request METHOD PATH [JSON] | http METHOD URL [JSON] | caddy-check"

func Run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "prepare-packages":
		if len(args) != 3 {
			return errors.New(usage)
		}
		return PreparePackages(ctx, args[1], args[2])
	case "unpack-packages":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return UnpackPackages(ctx, "/work")
	case "archive":
		if len(args) != 1 && len(args) != 2 {
			return errors.New(usage)
		}
		root := filepath.Join("bin", "network-e2e")
		if len(args) == 2 {
			root = args[1]
		}
		return Archive(root)
	case "control":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return RunControl(ctx, "/work")
	case "request", "http":
		if len(args) != 3 && len(args) != 4 {
			return errors.New(usage)
		}
		body := ""
		if len(args) == 4 {
			body = args[3]
		}
		token := ""
		url := args[2]
		if args[0] == "request" {
			data, err := os.ReadFile("/work/token-output")
			if err != nil {
				return fmt.Errorf("read API token: %w", err)
			}
			lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
			token = strings.TrimSpace(lines[len(lines)-1])
			if token == "" {
				return errors.New("API token file is empty")
			}
			url = "http://127.0.0.1:8080/test-admin/api/" + args[2]
		}
		return HTTPRequest(ctx, &http.Client{Timeout: 25 * time.Second}, args[1], url, body, token, out)
	case "caddy-check":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return CaddyCheck(ctx, out)
	default:
		return fmt.Errorf("unknown command %q; %s", args[0], usage)
	}
}

func Archive(root string) error {
	archive := filepath.Join(root, "archive", strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(archive, 0755); err != nil {
		return err
	}
	for _, pattern := range []string{"panel.db*", "records.json", "events.jsonl", "token-output", "results.txt", "client-*", "debug*"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return err
		}
		for _, path := range matches {
			// A bind mount can retain directory entries for files already archived by the host.
			if err := os.Rename(path, filepath.Join(archive, filepath.Base(path))); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("archive %s: %w", path, err)
			}
		}
	}
	return nil
}
