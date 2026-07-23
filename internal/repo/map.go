// Package repo builds small, query-ranked repository maps. The map gives the
// model navigational context without paying to upload entire source files.
package repo

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuzu-ux/ycode/internal/token"
)

type Snapshot struct {
	Text            string
	FilesScanned    int
	FilesIncluded   int
	EstimatedTokens int
}

type Benchmark struct {
	Files               int
	NaiveContextTokens  int
	MapTokens           int
	AvoidedTokens       int
	ReductionPercentage float64
	MapBuildDuration    time.Duration
}

type candidate struct {
	path  string
	size  int64
	score int
}

var queryWord = regexp.MustCompile(`[A-Za-z0-9_./-]+`)

// Build returns a bounded repository map ranked for the current user request.
func Build(root, query string, maxTokens int) (Snapshot, error) {
	if maxTokens <= 0 {
		maxTokens = 1_200
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve workspace: %w", err)
	}
	paths, err := trackedAndVisibleFiles(absolute)
	if err != nil {
		return Snapshot{}, err
	}
	terms := queryTerms(query)
	candidates := make([]candidate, 0, len(paths))
	for _, path := range paths {
		if shouldSkip(path) {
			continue
		}
		info, err := os.Stat(filepath.Join(absolute, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, candidate{
			path:  path,
			size:  info.Size(),
			score: rank(path, terms),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})

	var output strings.Builder
	output.WriteString("YCode workspace map (query-ranked; inspect files before editing)\n")
	output.WriteString("root: ")
	output.WriteString(filepath.Base(absolute))
	output.WriteByte('\n')
	if summary := gitSummary(absolute); summary != "" {
		output.WriteString(summary)
		output.WriteByte('\n')
	}
	output.WriteString("files:\n")

	included := 0
	usedTokens := token.EstimateText(output.String())
	for _, item := range candidates {
		if maxTokens-usedTokens < 20 {
			break
		}
		line := "- " + item.path + " (" + formatSize(item.size) + ")"
		if usedTokens+token.EstimateText(line)+20 > maxTokens {
			continue
		}
		if symbols := extractSymbols(filepath.Join(absolute, filepath.FromSlash(item.path))); symbols != "" {
			line += " :: " + symbols
		}
		line += "\n"
		lineTokens := token.EstimateText(line)
		if usedTokens+lineTokens+20 > maxTokens {
			continue
		}
		output.WriteString(line)
		usedTokens += lineTokens
		included++
	}
	if hidden := len(candidates) - included; hidden > 0 {
		fmt.Fprintf(&output, "… %d more files omitted by the %d-token map budget\n", hidden, maxTokens)
	}

	text := output.String()
	if token.EstimateText(text) > maxTokens {
		text = token.Clip(text, maxTokens).Text
	}
	return Snapshot{
		Text:            text,
		FilesScanned:    len(candidates),
		FilesIncluded:   included,
		EstimatedTokens: token.EstimateText(text),
	}, nil
}

// Measure compares a naïve "send every text file" baseline with YCode's map.
// It uses file sizes only and never reads secret-like files.
func Measure(root, query string, mapTokens int) (Benchmark, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Benchmark{}, err
	}
	paths, err := trackedAndVisibleFiles(absolute)
	if err != nil {
		return Benchmark{}, err
	}
	var bytesTotal int64
	files := 0
	for _, path := range paths {
		if shouldSkip(path) || !isContextText(path) {
			continue
		}
		info, statErr := os.Stat(filepath.Join(absolute, filepath.FromSlash(path)))
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		bytesTotal += info.Size()
		files++
	}
	started := time.Now()
	snapshot, err := Build(absolute, query, mapTokens)
	if err != nil {
		return Benchmark{}, err
	}
	naive := int((bytesTotal + 3) / 4)
	avoided := naive - snapshot.EstimatedTokens
	if avoided < 0 {
		avoided = 0
	}
	reduction := 0.0
	if naive > 0 {
		reduction = float64(avoided) / float64(naive) * 100
	}
	return Benchmark{
		Files:               files,
		NaiveContextTokens:  naive,
		MapTokens:           snapshot.EstimatedTokens,
		AvoidedTokens:       avoided,
		ReductionPercentage: reduction,
		MapBuildDuration:    time.Since(started),
	}, nil
}

func trackedAndVisibleFiles(root string) ([]string, error) {
	command := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
	if data, err := command.Output(); err == nil {
		raw := bytes.Split(data, []byte{0})
		paths := make([]string, 0, len(raw))
		for _, path := range raw {
			if len(path) != 0 {
				paths = append(paths, filepath.ToSlash(string(path)))
			}
		}
		return paths, nil
	}

	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	return paths, nil
}

func queryTerms(query string) []string {
	seen := make(map[string]struct{})
	var terms []string
	for _, match := range queryWord.FindAllString(strings.ToLower(query), -1) {
		match = strings.Trim(match, "./-_")
		if len(match) < 3 {
			continue
		}
		if _, exists := seen[match]; exists {
			continue
		}
		seen[match] = struct{}{}
		terms = append(terms, match)
		if len(terms) == 16 {
			break
		}
	}
	return terms
}

func rank(path string, terms []string) int {
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	depth := strings.Count(path, "/")
	score := 12 - min(depth, 10)

	switch base {
	case "agents.md", "ycode.md", "readme.md", "go.mod", "package.json", "cargo.toml", "pyproject.toml":
		score += 18
	}
	if isSource(path) {
		score += 4
	}
	for _, term := range terms {
		if base == term || strings.TrimSuffix(base, filepath.Ext(base)) == term {
			score += 50
		} else if strings.Contains(base, term) {
			score += 30
		} else if strings.Contains(lower, term) {
			score += 15
		}
	}
	return score
}

func extractSymbols(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 || !isSource(path) {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	var symbols []string
	lineNumber := 0
	for scanner.Scan() && lineNumber < 500 && len(symbols) < 5 {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if symbolLine(line) {
			line = strings.TrimSuffix(line, "{")
			line = strings.TrimSpace(line)
			if len(line) > 90 {
				line = line[:90] + "…"
			}
			symbols = append(symbols, strconv.Itoa(lineNumber)+":"+line)
		}
	}
	return strings.Join(symbols, "; ")
}

var symbolPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^(pub\s+)?(async\s+)?(fn|struct|enum|trait|type|class|interface)\s+[A-Za-z_]`),
	regexp.MustCompile(`^(export\s+)?(default\s+)?(async\s+)?(function|class|interface|type)\s+[A-Za-z_$]`),
	regexp.MustCompile(`^(export\s+)?(const|let)\s+[A-Za-z_$][A-Za-z0-9_$]*\s*=`),
	regexp.MustCompile(`^(async\s+)?(def|class)\s+[A-Za-z_]`),
	regexp.MustCompile(`^(func|type)\s+[A-Za-z_]`),
	regexp.MustCompile(`^func\s+\([^)]*\)\s+[A-Za-z_]`),
	regexp.MustCompile(`^(public\s+|private\s+|internal\s+)?(final\s+)?(class|struct|enum|protocol|func)\s+[A-Za-z_]`),
}

func symbolLine(line string) bool {
	if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
		return false
	}
	for _, pattern := range symbolPatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func gitSummary(root string) string {
	command := exec.Command("git", "-C", root, "status", "--short", "--branch", "--untracked-files=no")
	data, err := command.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	branch := strings.TrimSpace(strings.TrimPrefix(lines[0], "##"))
	changes := len(lines) - 1
	if changes == 0 {
		return "git: " + branch + " (clean)"
	}
	return fmt.Sprintf("git: %s (%d tracked changes)", branch, changes)
}

func formatSize(size int64) string {
	switch {
	case size < 1_024:
		return fmt.Sprintf("%dB", size)
	case size < 1_024*1_024:
		return fmt.Sprintf("%.1fKB", float64(size)/1_024)
	default:
		return fmt.Sprintf("%.1fMB", float64(size)/(1_024*1_024))
	}
}

func isSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".rs", ".py", ".js", ".jsx", ".ts", ".tsx", ".swift", ".java", ".kt", ".kts", ".rb", ".php", ".c", ".h", ".cc", ".cpp", ".cs":
		return true
	default:
		return false
	}
}

func isContextText(path string) bool {
	if isSource(path) {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".scss", ".sql", ".sh", ".zsh", ".fish":
		return true
	}
	switch strings.ToLower(filepath.Base(path)) {
	case "makefile", "dockerfile", "license", "go.mod", "go.sum":
		return true
	default:
		return false
	}
}

func shouldSkip(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, part := range strings.Split(lower, "/") {
		if ignoredDirectory(part) {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(lower))
	if base == ".env" || strings.HasPrefix(base, ".env.") ||
		strings.Contains(base, "credentials") || strings.Contains(base, "secret") ||
		base == "id_rsa" || base == "id_ed25519" {
		return true
	}
	return false
}

func ignoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".ycode", "node_modules", "vendor", "target", "dist", "build", ".next", ".cache", "coverage":
		return true
	default:
		return false
	}
}
