package interp

import (
	"encoding/csv"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/LackOfMorals/graphlite/v2/cypher/syntax"
)

// csvSource is the state LOAD CSV exposes to file() and linenumber().
type csvSource struct {
	file string
	line int64
}

// resolveCSVPath maps a LOAD CSV URL to a file inside the import directory.
// Only file URLs (file:///name.csv) and plain relative paths are accepted;
// nothing outside the directory can be read.
func (ex *exec) resolveCSVPath(raw string) (string, error) {
	if ex.g.eng == nil || ex.g.eng.ImportDir == "" {
		return "", errorf("ConfigurationError", "LoadCSVDisabled",
			"LOAD CSV is disabled: open the database with an import directory (graphlite.WithImportDirectory)")
	}
	path := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "file" {
			return "", errorf("ConfigurationError", "LoadCSVUnsupportedURL", "LOAD CSV only reads files (file:///name.csv); got %q", raw)
		}
		path = u.Path
	}
	root, err := filepath.Abs(ex.g.eng.ImportDir)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.Clean("/"+path))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errorf("ConfigurationError", "LoadCSVOutsideImportDir", "%q is outside the import directory", raw)
	}
	// A symbolic link inside the import directory must not lead out of it.
	if real, err := filepath.EvalSymlinks(full); err == nil {
		realRoot, rerr := filepath.EvalSymlinks(root)
		if rerr != nil {
			realRoot = root
		}
		if r, err := filepath.Rel(realRoot, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", errorf("ConfigurationError", "LoadCSVOutsideImportDir", "%q is outside the import directory", raw)
		}
	}
	return full, nil
}

func (ex *exec) execLoadCSV(cl *syntax.LoadCSV, st *qstate) error {
	st.declare(cl.Var)
	var out []row
	for _, r := range st.rows {
		from, err := ex.eval(cl.From, r)
		if err != nil {
			return err
		}
		raw, ok := from.(string)
		if !ok {
			return typeErr("LOAD CSV expects a URL string, got %s", typeName(from))
		}
		comma := ','
		if cl.FieldTerminator != nil {
			ft, err := ex.eval(cl.FieldTerminator, r)
			if err != nil {
				return err
			}
			s, ok := ft.(string)
			if !ok || len([]rune(s)) != 1 {
				return argErr("FIELDTERMINATOR must be a single character")
			}
			comma = []rune(s)[0]
		}
		path, err := ex.resolveCSVPath(raw)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return errorf("ArgumentError", "LoadCSVFileNotFound", "cannot open %q: %v", raw, err)
		}
		rd := csv.NewReader(f)
		rd.Comma = comma
		rd.FieldsPerRecord = -1
		rd.LazyQuotes = true
		var header []string
		line := int64(0)
		for {
			rec, err := rd.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.Close()
				return errorf("ArgumentError", "LoadCSVMalformed", "%q: %v", raw, err)
			}
			line++
			cell := func(s string) any {
				if s == "" {
					return nil
				}
				return s
			}
			if cl.WithHeaders && header == nil {
				header = rec
				continue
			}
			var val any
			if cl.WithHeaders {
				m := make(map[string]any, len(header))
				for i, h := range header {
					if i < len(rec) {
						m[h] = cell(rec[i])
					} else {
						m[h] = nil
					}
				}
				val = m
			} else {
				l := make([]any, len(rec))
				for i, s := range rec {
					l[i] = cell(s)
				}
				val = l
			}
			nr := r.with(cl.Var, val)
			nr = nr.with(csvFileKey, raw).with(csvLineKey, line)
			out = append(out, nr)
		}
		f.Close()
	}
	st.rows = out
	return nil
}

// Hidden row entries that carry the current CSV file and line for file() and
// linenumber().
const (
	csvFileKey = "\x00csvfile"
	csvLineKey = "\x00csvline"
)
