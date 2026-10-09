package analyze

import "strings"

// funcInfo describes a built-in function.
type funcInfo struct {
	ret           typ
	aggregate     bool
	nondeterminic bool
}

func ret(t typ) funcInfo { return funcInfo{ret: t} }

var tListString = listOf(tString)

// builtins maps a lower-cased function name to its description. The set is the
// openCypher / Neo4j 2025 function library; unknown names are UnknownFunction.
var builtins = map[string]funcInfo{
	// aggregating
	"count": {ret: tInt, aggregate: true}, "sum": {aggregate: true}, "avg": {aggregate: true},
	"min": {aggregate: true}, "max": {aggregate: true}, "collect": {ret: typ{k: kList}, aggregate: true},
	"percentilecont": {ret: tFloat, aggregate: true}, "percentiledisc": {aggregate: true},
	"stdev": {ret: tFloat, aggregate: true}, "stdevp": {ret: tFloat, aggregate: true},

	// scalar
	"coalesce": ret(tAny), "elementid": ret(tString), "endnode": ret(tNode), "startnode": ret(tNode),
	"head": ret(tAny), "last": ret(tAny), "id": ret(tInt), "length": ret(tInt), "size": ret(tInt),
	"nullif": ret(tAny), "properties": ret(tMap), "timestamp": ret(tInt), "type": ret(tString),
	"valuetype": ret(tString), "exists": ret(tBool), "isempty": ret(tBool), "isnan": ret(tBool),
	"randomuuid": {ret: tString, nondeterminic: true}, "rand": {ret: tFloat, nondeterminic: true},

	// conversion
	"toboolean": ret(tBool), "tobooleanornull": ret(tBool), "tofloat": ret(tFloat), "tofloatornull": ret(tFloat),
	"tointeger": ret(tInt), "tointegerornull": ret(tInt), "tostring": ret(tString), "tostringornull": ret(tString),
	"tobooleanlist": ret(listOf(tBool)), "tofloatlist": ret(listOf(tFloat)),
	"tointegerlist": ret(listOf(tInt)), "tostringlist": ret(tListString),

	// list
	"keys": ret(tListString), "labels": ret(tListString), "nodes": ret(listOf(tNode)),
	"relationships": ret(listOf(tRel)), "rels": ret(listOf(tRel)), "range": ret(listOf(tInt)),
	"reverse": ret(tAny), "tail": ret(typ{k: kList}),

	// mathematical
	"abs": ret(tAny), "ceil": ret(tAny), "floor": ret(tAny), "round": ret(tAny), "sign": ret(tAny),
	"sqrt": ret(tFloat), "exp": ret(tFloat), "log": ret(tFloat), "log10": ret(tFloat), "e": ret(tFloat),
	"pi": ret(tFloat), "sin": ret(tFloat), "cos": ret(tFloat), "tan": ret(tFloat), "cot": ret(tFloat),
	"asin": ret(tFloat), "acos": ret(tFloat), "atan": ret(tFloat), "atan2": ret(tFloat),
	"degrees": ret(tFloat), "radians": ret(tFloat), "haversin": ret(tFloat),
	"sinh": ret(tFloat), "cosh": ret(tFloat), "tanh": ret(tFloat), "coth": ret(tFloat),

	// string
	"left": ret(tString), "right": ret(tString), "ltrim": ret(tString), "rtrim": ret(tString),
	"trim": ret(tString), "btrim": ret(tString), "replace": ret(tString), "substring": ret(tString),
	"tolower": ret(tString), "toupper": ret(tString), "lower": ret(tString), "upper": ret(tString),
	"split": ret(tListString), "char_length": ret(tInt), "character_length": ret(tInt),
	"normalize": ret(tString),

	// temporal, spatial, vector
	"date": ret(tAny), "datetime": ret(tAny), "localdatetime": ret(tAny), "localtime": ret(tAny),
	"time": ret(tAny), "duration": ret(tAny), "point": ret(tAny), "vector": ret(tAny),
	"vector_distance": ret(tFloat), "vector_norm": ret(tFloat), "vector_dimension_count": ret(tInt),
}

// namespaces whose member functions are accepted without listing each one.
var functionNamespaces = map[string]bool{
	"date": true, "datetime": true, "localdatetime": true, "localtime": true, "time": true,
	"duration": true, "point": true, "vector": true, "graph": true,
}

// lookupFunction resolves a function by namespace and name.
func lookupFunction(namespace []string, name string) (funcInfo, bool) {
	if len(namespace) > 0 {
		if functionNamespaces[strings.ToLower(namespace[0])] {
			return ret(tAny), true
		}
		return funcInfo{}, false
	}
	fi, ok := builtins[strings.ToLower(name)]
	return fi, ok
}
