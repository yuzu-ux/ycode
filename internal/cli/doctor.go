package cli

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuzu-ux/ycode/internal/config"
	"github.com/yuzu-ux/ycode/internal/textsafe"
)

func runDoctor(args []string, io streams) int {
	root := scanStringFlag(args, "root", ".")
	cfg, sources, err := config.Load(root)
	if err != nil {
		return fail(io.err, err)
	}
	flags := flag.NewFlagSet("ycode doctor", flag.ContinueOnError)
	flags.SetOutput(io.err)
	flags.StringVar(&root, "root", root, "workspace root")
	network := flags.Bool("network", false, "test the provider /models endpoint")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	failures := 0
	absolute, err := filepath.Abs(root)
	if err != nil {
		failures += doctorLine(io.out, false, "workspace", err.Error())
	} else if info, statErr := os.Stat(absolute); statErr != nil || !info.IsDir() {
		failures += doctorLine(io.out, false, "workspace", "not a readable directory")
	} else {
		doctorLine(io.out, true, "workspace", absolute)
	}
	for _, binary := range []string{"git"} {
		path, lookupErr := exec.LookPath(binary)
		if lookupErr != nil {
			failures += doctorLine(io.out, false, binary, "not on PATH")
		} else {
			doctorLine(io.out, true, binary, path)
		}
	}
	if path, lookupErr := exec.LookPath("rg"); lookupErr != nil {
		doctorLine(io.out, true, "search", "built-in search active (rg is optional)")
	} else {
		doctorLine(io.out, true, "search", "rg available at "+path)
	}

	doctorLine(io.out, true, "provider", cfg.Provider.BaseURL)
	doctorLine(io.out, true, "model", cfg.Provider.Model)
	if cfg.APIKey() == "" && requiresKey(cfg.Provider.BaseURL) {
		failures += doctorLine(io.out, false, "API key", "set YCODE_API_KEY or "+cfg.Provider.APIKeyEnv)
	} else if cfg.APIKey() != "" && !secureForCredential(cfg.Provider.BaseURL) {
		failures += doctorLine(io.out, false, "API transport", "refusing credential over non-loopback HTTP")
	} else if cfg.APIKey() == "" {
		doctorLine(io.out, true, "API key", "not required for loopback provider")
	} else {
		doctorLine(io.out, true, "API key", "found (value hidden)")
	}
	if sources.Global != "" {
		doctorLine(io.out, true, "global config", presentOrMissing(sources.Global))
	}
	if sources.Project != "" {
		doctorLine(io.out, true, "project config", presentOrMissing(sources.Project))
	}
	doctorLine(io.out, true, "token budget", fmt.Sprintf("%d input / %d map / %d tool output", cfg.Agent.InputBudgetTokens, cfg.Agent.RepoMapTokens, cfg.Agent.ToolOutputTokens))

	if *network {
		if err := probeProvider(cfg); err != nil {
			failures += doctorLine(io.out, false, "provider network", err.Error())
		} else {
			doctorLine(io.out, true, "provider network", "/models responded successfully")
		}
	}
	if failures != 0 {
		return 1
	}
	return 0
}

func doctorLine(writer io.Writer, ok bool, label, detail string) int {
	marker := "✓"
	if !ok {
		marker = "✗"
	}
	_, _ = fmt.Fprintf(writer, "%s %-18s %s\n", marker, textsafe.Terminal(label), textsafe.Terminal(detail))
	if ok {
		return 0
	}
	return 1
}

func presentOrMissing(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return path + " (not created; defaults active)"
}

func probeProvider(cfg config.Config) error {
	endpoint := strings.TrimRight(cfg.Provider.BaseURL, "/") + "/models"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if key := cfg.APIKey(); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", endpoint, response.Status)
	}
	return nil
}
