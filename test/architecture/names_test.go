package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// sharedPackageNames are reviewed exceptions to unique package names: the packages are never imported
// together, so no file needs an alias to tell them apart.
var sharedPackageNames = map[string]string{
	"panel": "internal/clients/panel is the agent's panel client and internal/model/panel the panel's " +
		"models; the agent must not import the panel models (cmd/agent/deps_test.go)",
}

// domainRepositories hold the SQLite repository of each domain. A domain repository is named like the
// domain, so it may share its name with the service of that domain; files that use both import the
// repository under an alias.
const domainRepositories = module + "internal/repository/sqlite/"

// goFiles walks the module code, skipping generated contracts.
func goFiles(t *testing.T, visit func(path string)) {
	t.Helper()

	for _, root := range []string{"../../cmd", "../../internal", "../../migrations", "../../test"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "pb" || name == "v2raystats" || name == "testdata" {
					return filepath.SkipDir
				}

				return nil
			}
			if strings.HasSuffix(path, ".go") {
				visit(path)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A package is imported under its own name, so the name at a call site always identifies it. Tests
// may dot-import a fixture package.
func TestModuleImportsHaveNoAlias(t *testing.T) {
	goFiles(t, func(path string) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			dep, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if im.Name == nil || !strings.HasPrefix(dep, module) {
				continue
			}
			switch {
			case im.Name.Name == "_":
			case im.Name.Name == "." && strings.HasSuffix(path, "_test.go"):
			case strings.HasPrefix(dep, domainRepositories):
			default:
				t.Errorf("%s imports %s as %q: import it under its package name", path, dep, im.Name.Name)
			}
		}
	})
}

// Unique package names keep imports free of aliases and make a qualifier name one package.
func TestPackageNamesAreUnique(t *testing.T) {
	dirs := map[string]map[string]bool{} // package name -> directories
	goFiles(t, func(path string) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name == "main" {
			return
		}
		if dirs[f.Name.Name] == nil {
			dirs[f.Name.Name] = map[string]bool{}
		}
		dirs[f.Name.Name][strings.TrimPrefix(filepath.ToSlash(filepath.Dir(path)), "../../")] = true
	})

	for name, set := range dirs {
		if len(set) < 2 || sharedPackageNames[name] != "" || domainRepositoryOfService(name, set) {
			continue
		}
		var list []string
		for d := range set {
			list = append(list, d)
		}
		sort.Strings(list)
		t.Errorf("package name %q is used by %s", name, strings.Join(list, ", "))
	}
}

// domainRepositoryOfService reports whether the packages named name are exactly a domain repository and
// the service of the same domain.
func domainRepositoryOfService(name string, dirs map[string]bool) bool {
	repository := strings.TrimPrefix(domainRepositories, module) + name

	return len(dirs) == 2 && dirs[repository] && dirs["internal/service/"+name]
}
