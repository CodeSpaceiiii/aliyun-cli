package starplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.starlark.net/starlark"
)

// FindPlugin searches for a star plugin by product name.
// Search order: ALIBABA_CLOUD_STAR_PLUGIN_PATH env var, then ~/.aliyun/star-plugins/
func FindPlugin(productName string) (*PluginMeta, string, error) {
	searchPaths := getSearchPaths()
	for _, base := range searchPaths {
		pluginDir := filepath.Join(base, productName)
		metaPath := filepath.Join(pluginDir, "plugin.json")
		if _, err := os.Stat(metaPath); err == nil {
			meta, err := loadPluginMeta(metaPath)
			if err != nil {
				return nil, "", fmt.Errorf("failed to load plugin.json: %w", err)
			}
			return meta, pluginDir, nil
		}
	}
	return nil, "", fmt.Errorf("star plugin '%s' not found in search paths: %v", productName, searchPaths)
}

func getSearchPaths() []string {
	var paths []string
	if envPath := os.Getenv("ALIBABA_CLOUD_STAR_PLUGIN_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths, filepath.Join(home, ".aliyun", "star-plugins"))
	}
	return paths
}

func loadPluginMeta(path string) (*PluginMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta PluginMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// ListCommands scans the apis/<version>/ directory and returns all command declarations.
func ListCommands(pluginDir string, version string) ([]CommandDecl, error) {
	apiDir := filepath.Join(pluginDir, "apis", version)
	entries, err := os.ReadDir(apiDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read api directory %s: %w", apiDir, err)
	}

	var commands []CommandDecl
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".star") {
			continue
		}
		starFile := filepath.Join(apiDir, entry.Name())
		cmd, err := loadCommandDecl(pluginDir, starFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load %s: %v\n", entry.Name(), err)
			continue
		}
		commands = append(commands, cmd)
	}
	return commands, nil
}

// loadCommandDecl loads a single .star file and calls command() to get its declaration.
func loadCommandDecl(pluginDir string, starFile string) (CommandDecl, error) {
	loader := newModuleLoader(pluginDir)
	thread := &starlark.Thread{
		Name: filepath.Base(starFile),
		Load: loader.load,
	}

	// Execute the file with host module available (for load-time use)
	hctx := &HostContext{Stdout: os.Stdout, Stderr: os.Stderr}
	predeclared := starlark.StringDict{
		"host": NewHostModule(hctx),
	}

	globals, err := starlark.ExecFile(thread, starFile, nil, predeclared)
	if err != nil {
		return CommandDecl{}, fmt.Errorf("exec %s: %w", starFile, err)
	}

	commandFn, ok := globals["command"]
	if !ok {
		return CommandDecl{}, fmt.Errorf("%s: missing command() function", starFile)
	}

	result, err := starlark.Call(thread, commandFn, nil, nil)
	if err != nil {
		return CommandDecl{}, fmt.Errorf("%s: calling command(): %w", starFile, err)
	}

	d, ok := result.(*starlark.Dict)
	if !ok {
		return CommandDecl{}, fmt.Errorf("%s: command() must return a dict", starFile)
	}

	return parseCommandDecl(dictToMap(d)), nil
}

// LoadStarFile loads a .star file and returns its globals.
func LoadStarFile(pluginDir string, starFile string, hctx *HostContext) (starlark.StringDict, error) {
	loader := newModuleLoader(pluginDir)
	thread := &starlark.Thread{
		Name: filepath.Base(starFile),
		Load: loader.load,
	}

	predeclared := starlark.StringDict{
		"host": NewHostModule(hctx),
	}

	globals, err := starlark.ExecFile(thread, starFile, nil, predeclared)
	if err != nil {
		return nil, fmt.Errorf("exec %s: %w", starFile, err)
	}
	return globals, nil
}

// moduleLoader implements the Starlark load() function with @shared resolution and caching.
type moduleLoader struct {
	pluginDir string
	cache     map[string]*cacheEntry
	mu        sync.Mutex
}

type cacheEntry struct {
	globals starlark.StringDict
	err     error
}

func newModuleLoader(pluginDir string) *moduleLoader {
	return &moduleLoader{
		pluginDir: pluginDir,
		cache:     make(map[string]*cacheEntry),
	}
}

func (ml *moduleLoader) load(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	ml.mu.Lock()
	if entry, ok := ml.cache[module]; ok {
		ml.mu.Unlock()
		return entry.globals, entry.err
	}
	ml.mu.Unlock()

	resolved := ml.resolvePath(thread, module)

	childThread := &starlark.Thread{
		Name: module,
		Load: ml.load,
	}

	// Provide host module to loaded files too
	hctx := &HostContext{Stdout: os.Stdout, Stderr: os.Stderr}
	predeclared := starlark.StringDict{
		"host": NewHostModule(hctx),
	}

	globals, err := starlark.ExecFile(childThread, resolved, nil, predeclared)

	ml.mu.Lock()
	ml.cache[module] = &cacheEntry{globals: globals, err: err}
	ml.mu.Unlock()

	return globals, err
}

func (ml *moduleLoader) resolvePath(thread *starlark.Thread, module string) string {
	// @shared/ prefix resolves to plugin root's _shared/ directory
	if strings.HasPrefix(module, "@shared/") {
		sharedPath := strings.TrimPrefix(module, "@shared/")
		// Look in the parent of the product plugin dir (the plugins root)
		pluginsRoot := filepath.Dir(ml.pluginDir)
		return filepath.Join(pluginsRoot, "_shared", sharedPath)
	}

	// Relative path: resolve relative to the current file being executed
	if thread != nil && thread.Name != "" {
		currentFile := thread.Name
		if filepath.IsAbs(currentFile) {
			return filepath.Join(filepath.Dir(currentFile), module)
		}
	}

	// Fallback: relative to plugin dir
	return filepath.Join(ml.pluginDir, module)
}

// ResolveCommandFile finds the .star file for a command name in the given version.
func ResolveCommandFile(pluginDir string, version string, cmdName string) (string, error) {
	// Convert kebab-case command name to file name (replace - with _)
	fileName := strings.ReplaceAll(cmdName, "-", "_") + ".star"
	starFile := filepath.Join(pluginDir, "apis", version, fileName)
	if _, err := os.Stat(starFile); err != nil {
		return "", fmt.Errorf("command '%s' not found (looked for %s)", cmdName, starFile)
	}
	return starFile, nil
}
