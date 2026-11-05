package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/artemlavr/giocondo/"

// Inspect production imports transitively, so a helper cannot accidentally bring
// storage or transport dependencies into the application core.
func TestDependencyBoundaries(t *testing.T) {
	type boundary struct {
		pkg       string
		forbidden []string
	}
	var boundaries []boundary
	for _, layer := range []string{"model", "service", "transport", "worker", "repository", "clients", "platform"} {
		root := filepath.Join("../../internal", layer)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}

			files, e := os.ReadDir(path)
			if e != nil {
				return e
			}

			hasGo := false
			for _, f := range files {
				if strings.HasSuffix(f.Name(), ".go") && !strings.HasSuffix(f.Name(), "_test.go") {
					hasGo = true
				}
			}

			if !hasGo {
				return nil
			}

			boundaries = append(boundaries, boundary{strings.TrimPrefix(filepath.ToSlash(path), "../../"), forbiddenFor(layer)})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range boundaries {
		t.Run(tc.pkg, func(t *testing.T) {
			seen := map[string]bool{}
			var visit func(string, string)
			visit = func(pkg, chain string) {
				if seen[pkg] {
					return
				}

				seen[pkg] = true
				if _, ok := allowedEdges[edge{from: tc.pkg, to: strings.TrimPrefix(pkg, module)}]; ok {
					return
				}
				for _, bad := range tc.forbidden {
					if pkg == bad || strings.HasPrefix(pkg, bad+"/") {
						t.Errorf("forbidden dependency: %s", chain)
						return
					}
				}
				if !strings.HasPrefix(pkg, module) {
					return
				}

				dir := filepath.Join("../..", strings.TrimPrefix(pkg, module))
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}

				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
						continue
					}
					f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ImportsOnly)
					if err != nil {
						t.Fatal(err)
					}

					for _, im := range f.Imports {
						dep, err := strconv.Unquote(im.Path.Value)
						if err != nil {
							t.Fatal(err)
						}

						visit(dep, chain+" -> "+dep)
					}
				}
			}
			visit(module+tc.pkg, tc.pkg)
		})
	}
}

// forbiddenFor lists the import paths a production package of layer must not reach, even transitively.
// Adapters (repository, clients, platform) depend on models only: the services own the ports they
// implement, and the application wires them together.
func forbiddenFor(layer string) []string {
	// Generated contracts are shared by both ends of a link, so adapters may use them.
	transportExceptContracts := []string{
		module + "internal/transport/http",
		module + "internal/transport/grpc/nodehub",
		module + "internal/transport/grpc/nodeauth",
	}

	switch layer {
	case "repository":
		return []string{
			module + "internal/app", module + "test", module + "internal/service", module + "internal/worker",
			module + "internal/transport", module + "internal/clients", module + "internal/platform", module + "deploy",
		}
	case "clients":
		return append([]string{
			module + "internal/app", module + "test", module + "internal/service", module + "internal/worker",
			module + "internal/repository", module + "internal/platform", module + "deploy", "modernc.org/sqlite",
		}, transportExceptContracts...)
	case "platform":
		return append([]string{
			module + "internal/app", module + "test", module + "internal/service", module + "internal/worker",
			module + "internal/repository", "modernc.org/sqlite",
		}, transportExceptContracts...)
	}

	bad := []string{module + "internal/app", module + "test", module + "internal/repository", "modernc.org/sqlite"}
	if layer != "worker" {
		bad = append(bad, module+"internal/worker")
	}
	if layer != "transport" {
		bad = append(bad, module+"internal/transport", module+"internal/platform", module+"internal/clients", module+"deploy")
	}
	if layer == "model" {
		bad = append(bad, module+"internal/service", "database/sql", "net/http", "google.golang.org/grpc")
	}

	return bad
}

type edge struct{ from, to string }

// allowedEdges are reviewed exceptions to forbiddenFor, each with its reason. The import is not
// inspected further.
var allowedEdges = map[edge]string{
	{from: "internal/platform/singboxctl", to: "internal/service/engine"}: "the sing-box driver implements " +
		"engine.Driver and reports engine.Traffic: the engine owns that port",
}

// sharedServicePackages are imported by other services for shared primitives and stateless
// config building, not for another service's behavior.
var sharedServicePackages = map[string]bool{
	module + "internal/service/operation":  true,
	module + "internal/service/singbox":    true,
	module + "internal/service/nodeconfig": true,
}

// A service reaches another service through a small interface it declares and the application wires,
// so peer services stay independent and their packages can change without rippling through imports.
func TestServicesDoNotImportPeerServices(t *testing.T) {
	const serviceRoot = module + "internal/service/"

	err := filepath.WalkDir("../../internal/service", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		self := module + strings.TrimPrefix(filepath.ToSlash(filepath.Dir(path)), "../../")

		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, im := range f.Imports {
			dep, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				return err
			}

			if strings.HasPrefix(dep, serviceRoot) && dep != self && !sharedServicePackages[dep] {
				t.Errorf("%s imports peer service %s", strings.TrimPrefix(filepath.ToSlash(path), "../../"), dep)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Transport adapters deliver configs prepared by services; they must not build them, so the
// preparation packages are not imported directly (a service may still use them underneath).
func TestTransportDoesNotPrepareConfigs(t *testing.T) {
	forbidden := []string{module + "internal/service/nodeconfig", module + "internal/service/singbox"}

	err := filepath.WalkDir("../../internal/transport", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, im := range f.Imports {
			dep, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				return err
			}

			for _, bad := range forbidden {
				if dep == bad {
					t.Errorf("%s imports %s", strings.TrimPrefix(filepath.ToSlash(path), "../../"), dep)
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
