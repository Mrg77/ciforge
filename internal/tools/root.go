package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

// projectRoot confines every filesystem action to one directory. SetProjectRoot
// is called once from main; an empty root means "current directory".
var projectRoot string

// SetProjectRoot fixes the directory the agent may operate in.
func SetProjectRoot(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		projectRoot = filepath.Clean(abs)
	}
}

// confine resolves a model-supplied path against the project root and refuses
// anything that escapes it. A model that asks for "../../etc/passwd" gets an
// error, not a traversal.
func confine(p string) (string, error) {
	root := projectRoot
	if root == "" {
		root, _ = filepath.Abs(".")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	rootClean := filepath.Clean(root)
	if abs != rootClean && !strings.HasPrefix(abs, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the allowed project root %q — ciforge won't operate there", p, rootClean)
	}
	return abs, nil
}

// truncate keeps tool output within a sane token budget: findings matter more
// than the tail of a long log.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n… (%d more bytes truncated)", len(s)-max)
}
