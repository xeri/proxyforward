package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Citation grammar, inside a single inline backtick span:
//
//	`path/to/file.go`               file must exist (matched by path suffix)
//	`file.go:12` / `file.go:22-35,196`  file must exist and have ≥ max cited line
//	`file.go SymbolName`            file must exist and contain "SymbolName"
//
// Spans containing \ % < > $ * or :// are runtime paths, placeholders, shell
// redirection, or URLs — skipped. Fenced code blocks are not scanned (they hold
// commands and diagrams, not citations). Wrapped spans (a citation split across
// a line break) are joined before scanning.
//
// Independently of spans, every TestXxx / FuzzXxx word anywhere in a doc must
// appear in some *_test.go file. The check is substring, so `-run` prefixes
// like TestBurst match TestBurstThroughputAndCrossStreamLatency.

var (
	fenceRe = regexp.MustCompile("(?s)```.*?```")
	wrapRe  = regexp.MustCompile(`\n[ \t]*`)
	spanRe  = regexp.MustCompile("`([^`]+)`")
	// fileRe: one whitespace-separated token naming a repo file, with an
	// optional :line[,range] suffix. Extensions limited to source/doc types so
	// prose like "vX.Y.Z" or runtime names like ".pfsetup" never match.
	fileRe  = regexp.MustCompile(`^([A-Za-z0-9_.][A-Za-z0-9_./-]*\.(?:go|tsx|ts|css|md|toml|json|yml))(:[0-9,\-]+)?$`)
	identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	testRe  = regexp.MustCompile(`\b(?:Test|Fuzz)[A-Z][A-Za-z0-9_]*`)
)

// Runtime artifacts the docs legitimately name that never exist in the tree.
var ignoredFiles = map[string]bool{
	"stats.json":          true,
	"stats.redacted.json": true,
	"config.toml":         true,
	"gateway_agents.json": true, // the gateway's per-agent allowlist, written at runtime
}

// Placeholder test names used in prose ("go test -run TestX ...").
var ignoredTests = map[string]bool{"TestX": true}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found at %s: %v", root, err)
	}
	return root
}

// listRepoFiles returns every file path (slash-separated, repo-relative),
// skipping VCS and dependency/output trees.
func listRepoFiles(t *testing.T, root string) []string {
	t.Helper()
	skip := map[string]bool{".git": true, "node_modules": true, "dist": true}
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	return files
}

// docFiles is the set of agent docs whose citations are enforced.
func docFiles(t *testing.T, root string) []string {
	t.Helper()
	docs := []string{filepath.Join(root, "CLAUDE.md")}
	for _, pattern := range []string{
		filepath.Join(root, "docs", "agent", "*.md"),
		filepath.Join(root, ".claude", "rules", "*.md"),
		filepath.Join(root, ".claude", "skills", "*", "SKILL.md"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		docs = append(docs, matches...)
	}
	if len(docs) < 8 {
		t.Fatalf("expected the full doc suite (root + docs/agent + rules + skills), found only %d files", len(docs))
	}
	return docs
}

func candidates(token string, files []string) []string {
	token = strings.TrimPrefix(token, "./")
	var out []string
	for _, f := range files {
		if f == token || strings.HasSuffix(f, "/"+token) {
			out = append(out, f)
		}
	}
	return out
}

// fileContent caches cited-file contents per test run.
type fileContent struct {
	root  string
	cache map[string]string
}

func (fc *fileContent) get(t *testing.T, rel string) string {
	t.Helper()
	if s, ok := fc.cache[rel]; ok {
		return s
	}
	b, err := os.ReadFile(filepath.Join(fc.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	fc.cache[rel] = string(b)
	return string(b)
}

func TestDocCitations(t *testing.T) {
	root := repoRoot(t)
	files := listRepoFiles(t, root)
	fc := &fileContent{root: root, cache: map[string]string{}}

	for _, doc := range docFiles(t, root) {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		name, _ := filepath.Rel(root, doc)
		body := fenceRe.ReplaceAllString(string(raw), "")
		body = wrapRe.ReplaceAllString(body, " ")

		for _, m := range spanRe.FindAllStringSubmatch(body, -1) {
			span := m[1]
			if strings.ContainsAny(span, `\%<>$*`) || strings.Contains(span, "://") {
				continue
			}
			current := "" // most recent file token in this span
			for _, tok := range strings.Fields(span) {
				if fm := fileRe.FindStringSubmatch(tok); fm != nil {
					if ignoredFiles[filepath.Base(fm[1])] {
						current = ""
						continue
					}
					cands := candidates(fm[1], files)
					if len(cands) == 0 {
						t.Errorf("%s: cited file %q not found in the tree (span `%s`)", name, fm[1], span)
						current = ""
						continue
					}
					current = fm[1]
					if fm[2] != "" {
						checkLines(t, fc, name, span, fm[2], cands)
					}
					continue
				}
				if current != "" && identRe.MatchString(tok) {
					found := false
					for _, c := range candidates(current, files) {
						if strings.Contains(fc.get(t, c), tok) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%s: symbol %q not found in %s (span `%s`)", name, tok, current, span)
					}
				}
			}
		}
	}
}

// checkLines asserts the largest cited line number exists in some candidate.
func checkLines(t *testing.T, fc *fileContent, doc, span, suffix string, cands []string) {
	t.Helper()
	max := 0
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(suffix, ":"), func(r rune) bool {
		return r == ',' || r == '-'
	}) {
		n, err := strconv.Atoi(part)
		if err != nil {
			t.Errorf("%s: unparseable line citation %q (span `%s`)", doc, suffix, span)
			return
		}
		if n > max {
			max = n
		}
	}
	for _, c := range cands {
		if strings.Count(fc.get(t, c), "\n")+1 >= max {
			return
		}
	}
	t.Errorf("%s: line %d cited but %s is shorter (span `%s`)", doc, max, cands[0], span)
}

func TestDocTestNames(t *testing.T) {
	root := repoRoot(t)
	var testSrc strings.Builder
	for _, f := range listRepoFiles(t, root) {
		if strings.HasSuffix(f, "_test.go") {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			testSrc.Write(b)
		}
	}
	all := testSrc.String()

	for _, doc := range docFiles(t, root) {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		name, _ := filepath.Rel(root, doc)
		for _, tn := range testRe.FindAllString(string(raw), -1) {
			if ignoredTests[tn] {
				continue
			}
			if !strings.Contains(all, tn) {
				t.Errorf("%s: cites %s but no *_test.go contains it", name, tn)
			}
		}
	}
}
