package tools

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	maxEditableFile = 4 << 20
	maxSearchFile   = 1 << 20
)

type Workspace struct {
	root     string
	readOnly bool
}

type workspaceArgs struct {
	Action    string `json:"action"`
	Path      string `json:"path"`
	Query     string `json:"query"`
	Regex     bool   `json:"regex"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Depth     int    `json:"depth"`
	Limit     int    `json:"limit"`
	Content   string `json:"content"`
	Old       string `json:"old"`
	New       string `json:"new"`
	All       bool   `json:"all"`
}

func NewWorkspace(root string, readOnly bool) (*Workspace, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace root is not a directory")
	}
	return &Workspace{root: resolved, readOnly: readOnly}, nil
}

func (w *Workspace) Root() string {
	return w.root
}

func (w *Workspace) Execute(arguments string) (string, error) {
	var args workspaceArgs
	if err := decodeStrict(strings.NewReader(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid workspace arguments: %w", err)
	}
	switch args.Action {
	case "list":
		return w.list(args)
	case "read":
		return w.read(args)
	case "search":
		return w.search(args)
	case "stat":
		return w.stat(args)
	case "write":
		return w.write(args)
	case "replace":
		return w.replace(args)
	default:
		return "", fmt.Errorf("unsupported workspace action %q", args.Action)
	}
}

func (w *Workspace) list(args workspaceArgs) (string, error) {
	path, err := w.safePath(defaultPath(args.Path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("list path is not a directory")
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 2
	}
	if depth > 8 {
		depth = 8
	}
	limit := boundedLimit(args.Limit, 200)
	baseDepth := pathDepth(path)
	var entries []string
	err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if current == path {
			return nil
		}
		relativeDepth := pathDepth(current) - baseDepth
		if entry.IsDir() && ignoredToolDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if relativeDepth > depth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(w.root, current)
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			entries = append(entries, "d "+filepath.ToSlash(relative)+"/")
		} else if entry.Type().IsRegular() {
			if sensitiveToolPath(relative) {
				return nil
			}
			if info, err := entry.Info(); err == nil {
				entries = append(entries, "f "+filepath.ToSlash(relative)+" ("+strconv.FormatInt(info.Size(), 10)+"B)")
			}
		}
		if len(entries) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return "[empty directory]", nil
	}
	return strings.Join(entries, "\n"), nil
}

func (w *Workspace) read(args workspaceArgs) (string, error) {
	if args.Path == "" {
		return "", errors.New("read requires path")
	}
	path, err := w.safePath(args.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("read path is not a regular file")
	}
	if info.Size() > maxEditableFile {
		return "", fmt.Errorf("file is larger than %d bytes; read a narrower source file", maxEditableFile)
	}
	relative, _ := filepath.Rel(w.root, path)
	if sensitiveToolPath(relative) {
		return "", errors.New("reading secret-like files requires the shell approval path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return "", errors.New("binary files cannot be read as text")
	}
	lines := strings.Split(string(data), "\n")
	start := args.StartLine
	if start <= 0 {
		start = 1
	}
	end := args.EndLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", errors.New("end_line must be greater than or equal to start_line")
	}
	if end-start+1 > 500 {
		end = start + 499
	}
	if start > len(lines) {
		return fmt.Sprintf("[file has %d lines]", len(lines)), nil
	}
	width := len(strconv.Itoa(end))
	var output strings.Builder
	for index := start; index <= end; index++ {
		fmt.Fprintf(&output, "%*d | %s\n", width, index, lines[index-1])
	}
	return strings.TrimRight(output.String(), "\n"), nil
}

func (w *Workspace) search(args workspaceArgs) (string, error) {
	if strings.TrimSpace(args.Query) == "" {
		return "", errors.New("search requires query")
	}
	start, err := w.safePath(defaultPath(args.Path))
	if err != nil {
		return "", err
	}
	limit := boundedLimit(args.Limit, 100)

	var pattern *regexp.Regexp
	if args.Regex {
		pattern, err = regexp.Compile(args.Query)
		if err != nil {
			return "", fmt.Errorf("invalid regex: %w", err)
		}
	}
	needle := strings.ToLower(args.Query)
	var matches []string
	err = filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != start && ignoredToolDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, _ := filepath.Rel(w.root, path)
		if sensitiveToolPath(relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxSearchFile {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 32<<10), 1<<20)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			found := pattern != nil && pattern.MatchString(line)
			if pattern == nil {
				found = strings.Contains(strings.ToLower(line), needle)
			}
			if found {
				relative, _ := filepath.Rel(w.root, path)
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > 240 {
					trimmed = trimmed[:240] + "…"
				}
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(relative), lineNumber, trimmed))
				if len(matches) >= limit {
					break
				}
			}
		}
		file.Close()
		if len(matches) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "[no matches]", nil
	}
	return strings.Join(matches, "\n"), nil
}

func (w *Workspace) stat(args workspaceArgs) (string, error) {
	path, err := w.safePath(defaultPath(args.Path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	relative, _ := filepath.Rel(w.root, path)
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return fmt.Sprintf("path=%s\ntype=%s\nsize=%d\nmode=%s\nmodified=%s",
		filepath.ToSlash(relative), kind, info.Size(), info.Mode(), info.ModTime().UTC().Format("2006-01-02T15:04:05Z")), nil
}

func (w *Workspace) write(args workspaceArgs) (string, error) {
	if w.readOnly {
		return "", errors.New("workspace is read-only")
	}
	if args.Path == "" {
		return "", errors.New("write requires path")
	}
	if len(args.Content) > maxEditableFile {
		return "", fmt.Errorf("content exceeds %d-byte edit limit", maxEditableFile)
	}
	path, err := w.safePath(args.Path)
	if err != nil {
		return "", err
	}
	if protectedPath(w.root, path) {
		return "", errors.New("YCode will not edit Git internals")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWrite(path, []byte(args.Content), mode); err != nil {
		return "", err
	}
	relative, _ := filepath.Rel(w.root, path)
	return fmt.Sprintf("wrote %s (%d bytes)", filepath.ToSlash(relative), len(args.Content)), nil
}

func (w *Workspace) replace(args workspaceArgs) (string, error) {
	if w.readOnly {
		return "", errors.New("workspace is read-only")
	}
	if args.Path == "" || args.Old == "" {
		return "", errors.New("replace requires path and non-empty old text")
	}
	path, err := w.safePath(args.Path)
	if err != nil {
		return "", err
	}
	if protectedPath(w.root, path) {
		return "", errors.New("YCode will not edit Git internals")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxEditableFile {
		return "", fmt.Errorf("file exceeds %d-byte edit limit", maxEditableFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	count := strings.Count(string(data), args.Old)
	if count == 0 {
		return "", errors.New("old text was not found")
	}
	if count > 1 && !args.All {
		return "", fmt.Errorf("old text occurs %d times; provide more context or set all=true", count)
	}
	limit := 1
	if args.All {
		limit = -1
	}
	updated := strings.Replace(string(data), args.Old, args.New, limit)
	if len(updated) > maxEditableFile {
		return "", fmt.Errorf("updated file exceeds %d-byte edit limit", maxEditableFile)
	}
	if err := atomicWrite(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	relative, _ := filepath.Rel(w.root, path)
	replaced := 1
	if args.All {
		replaced = count
	}
	return fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, filepath.ToSlash(relative)), nil
}

func (w *Workspace) safePath(userPath string) (string, error) {
	if filepath.IsAbs(userPath) {
		return "", errors.New("path must be workspace-relative")
	}
	clean := filepath.Clean(userPath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the workspace")
	}
	candidate := filepath.Join(w.root, clean)
	relative, err := filepath.Rel(w.root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the workspace")
	}
	if protectedPath(w.root, candidate) {
		return "", errors.New("Git internals are unavailable through the workspace tool")
	}

	probe := candidate
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !inside(w.root, resolved) {
				return "", errors.New("symlink resolves outside the workspace")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return candidate, nil
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ycode-edit-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil && runtime.GOOS == "windows" {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tempPath, path)
	} else {
		return err
	}
}

func defaultPath(path string) string {
	if path == "" {
		return "."
	}
	return path
}

func boundedLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > 1_000 {
		return 1_000
	}
	return value
}

func pathDepth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator))
}

func ignoredToolDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".ycode", "node_modules", "vendor", "target", "dist", "build", ".next", ".cache":
		return true
	default:
		return false
	}
}

func protectedPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	return len(parts) > 0 && strings.EqualFold(parts[0], ".git")
}

func sensitiveToolPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == ".env" || (strings.HasPrefix(base, ".env.") && base != ".env.example") {
		return true
	}
	if base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".p12") {
		return true
	}
	return strings.Contains(base, "credential") || strings.Contains(base, "secret")
}
