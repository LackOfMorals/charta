package interp

import (
	"encoding/csv"
	"errors"
	"io"
	"io/fs"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/graingraph/graingraph/cypher/syntax"
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
			"LOAD CSV is disabled: open the database with an import directory (graingraph.WithImportDirectory)")
	}
	path := raw
	if strings.Contains(raw, "://") {
		// Parsed by hand rather than with net/url: only file URLs are accepted,
		// and a path is all that is needed from them.
		rest, ok := strings.CutPrefix(raw, "file://")
		if !ok {
			return "", errorf("ConfigurationError", "LoadCSVUnsupportedURL", "LOAD CSV only reads files (file:///name.csv); got %q", raw)
		}
		if !strings.HasPrefix(rest, "/") { // file://host/path: only localhost is local
			host, p, _ := strings.Cut(rest, "/")
			if host != "localhost" {
				return "", errorf("ConfigurationError", "LoadCSVUnsupportedURL", "LOAD CSV cannot read from host %q", host)
			}
			rest = "/" + p
		}
		decoded, err := neturl.PathUnescape(rest)
		if err != nil {
			return "", errorf("ConfigurationError", "LoadCSVUnsupportedURL", "bad file URL %q", raw)
		}
		path = decoded
	}
	root, err := filepath.Abs(ex.g.eng.ImportDir)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
			if isReservedDeviceName(part) {
				return "", errorf("ConfigurationError", "LoadCSVUnsupportedURL", "%q is not a file name", raw)
			}
		}
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
			// The OS error names the resolved server path, which is not the
			// client's to see.
			reason := "cannot be opened"
			switch {
			case errors.Is(err, fs.ErrNotExist):
				reason = "was not found"
			case errors.Is(err, fs.ErrPermission):
				reason = "is not readable"
			}
			return errorf("ArgumentError", "LoadCSVFileNotFound", "%q %s", raw, reason)
		}
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			f.Close() // a directory, or a device such as CON or COM1 that would block
			return errorf("ArgumentError", "LoadCSVFileNotFound", "%q is not a regular file", raw)
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
				var pe *csv.ParseError
				if errors.As(err, &pe) {
					return errorf("ArgumentError", "LoadCSVMalformed", "%q: %v", raw, err)
				}
				return errorf("ArgumentError", "LoadCSVMalformed", "%q could not be read", raw)
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

// isReservedDeviceName reports whether name is one of the Windows device names
// (CON, PRN, AUX, NUL, COM1-9, LPT1-9), with or without an extension: opening
// such a name under any directory opens the device, not a file.
func isReservedDeviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return true
	}
	return false
}
