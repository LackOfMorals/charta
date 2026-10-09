//go:build tck

package compat

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/cucumber/godog"

	"github.com/LackOfMorals/graphlite/v2/cypher/proc"
)

var procDecl = regexp.MustCompile(`^(\S+?)\((.*?)\)\s*::\s*\((.*?)\)\s*:?$`)

// parseParams parses "a :: INTEGER?, b :: STRING?".
func parseParams(s string) ([]proc.Param, error) {
	var out []proc.Param
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, part := range strings.Split(s, ",") {
		nt := strings.SplitN(part, "::", 2)
		if len(nt) != 2 {
			return nil, fmt.Errorf("bad procedure parameter %q", part)
		}
		typ, nullable := proc.ParseType(nt[1])
		out = append(out, proc.Param{Name: strings.TrimSpace(nt[0]), Type: typ, Nullable: nullable})
	}
	return out, nil
}

// givenProcedure registers a test procedure whose behaviour is a lookup table:
// the rows whose input cells equal the call arguments yield their output cells.
func (s *tckState) givenProcedure(ctx context.Context, decl string, table *godog.Table) error {
	if s.skipped || s.db == nil {
		return nil
	}
	m := procDecl.FindStringSubmatch(strings.TrimSpace(decl))
	if m == nil {
		return fmt.Errorf("cannot parse procedure declaration %q", decl)
	}
	ins, err := parseParams(m[2])
	if err != nil {
		return err
	}
	outs, err := parseParams(m[3])
	if err != nil {
		return err
	}
	type tableRow struct {
		in  []any
		out map[string]any
	}
	var rows []tableRow
	if table != nil && len(table.Rows) > 1 {
		for _, r := range table.Rows[1:] {
			if len(r.Cells) != len(ins)+len(outs) {
				return fmt.Errorf("procedure table row has %d cells, want %d", len(r.Cells), len(ins)+len(outs))
			}
			tr := tableRow{out: map[string]any{}}
			for i, c := range r.Cells {
				v, err := parseTV(c.Value)
				if err != nil {
					return err
				}
				if i < len(ins) {
					if v, err = proc.Coerce(ins[i], v); err != nil {
						return err
					}
					tr.in = append(tr.in, v)
				} else {
					tr.out[outs[i-len(ins)].Name] = v
				}
			}
			rows = append(rows, tr)
		}
	}
	s.db.RegisterProcedure(&proc.Procedure{
		Signature: proc.Signature{Name: m[1], Inputs: ins, Outputs: outs},
		Fn: func(_ context.Context, args []any) ([]map[string]any, error) {
			var res []map[string]any
			for _, tr := range rows {
				if len(tr.in) == len(args) && (len(args) == 0 || reflect.DeepEqual(tr.in, args)) {
					res = append(res, tr.out)
				}
			}
			return res, nil
		},
	})
	return nil
}
