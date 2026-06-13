package bigquery

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/m-mizutani/goerr/v2"
)

// runbookLoader loads SQL runbook files from configured paths.
type runbookLoader struct {
	paths []string
}

// newRunbookLoader creates a runbookLoader for the given paths.
func newRunbookLoader(paths []string) *runbookLoader {
	return &runbookLoader{paths: paths}
}

// loadRunbooks loads all SQL files from the configured paths and returns
// RunbookEntries.
func (r *runbookLoader) loadRunbooks() (RunbookEntries, error) {
	var entries RunbookEntries

	for _, path := range r.paths {
		pathEntries, err := r.loadFromPath(path)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to load runbooks from path",
				goerr.V("path", path))
		}
		entries = append(entries, pathEntries...)
	}

	return entries, nil
}

// loadFromPath loads runbooks from a single path which may be a file or
// directory.
func (r *runbookLoader) loadFromPath(path string) (RunbookEntries, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to stat path", goerr.V("path", path))
	}

	if info.IsDir() {
		return r.loadFromDirectory(path)
	}

	entry, err := r.loadFromFile(path)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil // not a .sql file
	}
	return RunbookEntries{entry}, nil
}

// loadFromDirectory recursively loads all .sql files under dirPath.
func (r *runbookLoader) loadFromDirectory(dirPath string) (RunbookEntries, error) {
	var entries RunbookEntries

	err := filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return goerr.Wrap(err, "failed to walk directory", goerr.V("path", path))
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".sql") {
			return nil
		}
		entry, err := r.loadFromFile(path)
		if err != nil {
			return err
		}
		if entry != nil {
			entries = append(entries, entry)
		}
		return nil
	})

	return entries, err
}

// loadFromFile parses a single .sql file into a RunbookEntry. Non-.sql files
// return (nil, nil).
func (r *runbookLoader) loadFromFile(filePath string) (*RunbookEntry, error) {
	if !strings.HasSuffix(strings.ToLower(filePath), ".sql") {
		return nil, nil
	}

	content, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read SQL file", goerr.V("path", filePath))
	}

	title, description := extractTitleAndDescription(string(content))
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(filePath), ".sql")
	}

	return &RunbookEntry{
		ID:          newRunbookID(),
		Title:       title,
		Description: description,
		SQLContent:  string(content),
	}, nil
}

var (
	titleRegex   = regexp.MustCompile(`^--\s*[Tt]itle\s*:\s*(.+)$`)
	descRegex    = regexp.MustCompile(`^--\s*[Dd]escription\s*:\s*(.+)$`)
	commentRegex = regexp.MustCompile(`^--\s*(.*)$`)
)

// extractTitleAndDescription extracts the title and description from leading
// SQL comment lines. The expected format is:
//
//	-- Title: Title of the runbook
//	-- Description: First line of description
//	-- Continuation of description
func extractTitleAndDescription(content string) (title, description string) {
	scanner := bufio.NewScanner(strings.NewReader(content))

	var desc strings.Builder
	var inDescription bool

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Stop at the first non-comment line.
		if !strings.HasPrefix(line, "--") {
			break
		}

		if matches := titleRegex.FindStringSubmatch(line); matches != nil {
			title = strings.TrimSpace(matches[1])
			continue
		}

		if matches := descRegex.FindStringSubmatch(line); matches != nil {
			inDescription = true
			if desc.Len() > 0 {
				desc.WriteString(" ")
			}
			desc.WriteString(strings.TrimSpace(matches[1]))
			continue
		}

		if inDescription {
			if matches := commentRegex.FindStringSubmatch(line); matches != nil {
				if desc.Len() > 0 {
					desc.WriteString(" ")
				}
				desc.WriteString(strings.TrimSpace(matches[1]))
			} else {
				break
			}
		}
	}

	return title, desc.String()
}
