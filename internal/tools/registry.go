// Package tools implements YCode's deliberately tiny two-tool surface.
// Fewer schemas mean fewer input tokens on every model call.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuzu-ux/ycode/internal/provider"
	"github.com/yuzu-ux/ycode/internal/textsafe"
)

type Approver func(command, reason string) bool

type Options struct {
	Root        string
	ReadOnly    bool
	ShellPolicy string
	Approver    Approver
}

type Registry struct {
	workspace *Workspace
	shell     *Shell
}

func New(options Options) (*Registry, error) {
	workspace, err := NewWorkspace(options.Root, options.ReadOnly)
	if err != nil {
		return nil, err
	}
	return &Registry{
		workspace: workspace,
		shell: &Shell{
			Root:     workspace.Root(),
			Policy:   options.ShellPolicy,
			Approver: options.Approver,
		},
	}, nil
}

func (r *Registry) Specs() []provider.ToolSpec {
	return []provider.ToolSpec{workspaceSpec(), shellSpec()}
}

func (r *Registry) Execute(ctx context.Context, call provider.ToolCall) (string, error) {
	switch call.Function.Name {
	case "workspace":
		return r.workspace.Execute(call.Function.Arguments)
	case "shell":
		return r.shell.Execute(ctx, call.Function.Arguments)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
}

func Summary(call provider.ToolCall) string {
	var values map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &values)
	switch call.Function.Name {
	case "workspace":
		action, _ := values["action"].(string)
		path, _ := values["path"].(string)
		if path == "" {
			path = "."
		}
		return textsafe.Terminal(strings.TrimSpace("workspace." + action + " " + path))
	case "shell":
		command, _ := values["command"].(string)
		if len(command) > 72 {
			command = command[:72] + "…"
		}
		return textsafe.Terminal("shell " + command)
	default:
		return textsafe.Terminal(call.Function.Name)
	}
}

func workspaceSpec() provider.ToolSpec {
	return provider.ToolSpec{
		Type: "function",
		Function: provider.FunctionSpec{
			Name:        "workspace",
			Description: "Inspect or edit files inside the workspace. Prefer search/read before replace/write. No delete action.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type": "string",
						"enum": []string{"list", "read", "search", "stat", "write", "replace"},
					},
					"path":       map[string]any{"type": "string", "description": "Workspace-relative path; defaults to ."},
					"query":      map[string]any{"type": "string", "description": "Text or regex for search"},
					"regex":      map[string]any{"type": "boolean"},
					"start_line": map[string]any{"type": "integer"},
					"end_line":   map[string]any{"type": "integer"},
					"depth":      map[string]any{"type": "integer"},
					"limit":      map[string]any{"type": "integer"},
					"content":    map[string]any{"type": "string", "description": "Full content for write"},
					"old":        map[string]any{"type": "string", "description": "Exact text to replace"},
					"new":        map[string]any{"type": "string", "description": "Replacement text"},
					"all":        map[string]any{"type": "boolean", "description": "Replace every match"},
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
	}
}

func shellSpec() provider.ToolSpec {
	return provider.ToolSpec{
		Type: "function",
		Function: provider.FunctionSpec{
			Name:        "shell",
			Description: "Run a command in the workspace. Read/build/test commands run directly; risky commands need approval or are blocked.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":    map[string]any{"type": "string"},
					"reason":     map[string]any{"type": "string", "description": "Short reason for commands that may need approval"},
					"timeout_ms": map[string]any{"type": "integer", "minimum": 100, "maximum": 120000},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}
