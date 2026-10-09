package analyze

import (
	"github.com/LackOfMorals/charta/cypher/proc"
	"github.com/LackOfMorals/charta/cypher/syntax"
)

// CheckParams reports a ParameterMissing error when a standalone CALL without
// an argument list needs a parameter that params does not supply.
func CheckParams(st *syntax.Statement, params map[string]any, reg proc.Registry) error {
	if !isStandaloneCall(st) {
		return nil
	}
	call := st.Body.(*syntax.SingleQuery).Clauses[0].(*syntax.Call)
	if !call.ArgsOmitted {
		return nil
	}
	c := &checker{reg: reg}
	sig := c.lookupProcedure(joinDots(call.Name))
	if sig == nil {
		return nil
	}
	for _, in := range sig.Inputs {
		if _, ok := params[in.Name]; !ok {
			return &Error{Class: ClassParameter, Code: CodeMissingParameter, Pos: call.Pos(),
				Msg: sprintf("expected parameter `%s`", in.Name)}
		}
	}
	return nil
}

func joinDots(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += "."
		}
		s += p
	}
	return s
}
