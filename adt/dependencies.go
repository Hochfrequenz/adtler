package adt

import (
	"context"
	"fmt"
	"strings"
)

// UseType classifies how a dependency is used by the queried object. The
// values are stable identifiers (not localised).
const (
	UseTypeTable       = "TABLE"
	UseTypeStructure   = "STRUCTURE"
	UseTypeDataElement = "DATA_ELEMENT"
	UseTypeDomain      = "DOMAIN"
	UseTypeView        = "VIEW"
	UseTypeTableType   = "TABLE_TYPE"
	UseTypeInterface   = "INTERFACE"
	UseTypeSuperclass  = "SUPERCLASS"
	UseTypeUnknown     = "UNKNOWN"
	// UseTypeUIApp is a UIAD app entry contained in a UIAC catalog.
	UseTypeUIApp = "UI_APP"
	// UseTypeTransaction is a transaction code launched by a UIAD app entry.
	UseTypeTransaction = "TRANSACTION"
	// UseTypeWebDynproApp is a Web Dynpro application launched by a UIAD app entry.
	UseTypeWebDynproApp = "WEB_DYNPRO_APP"
	// UseTypeWebClientTarget is a WebClient UI target launched by a UIAD app entry.
	UseTypeWebClientTarget = "WEB_CLIENT_TARGET"
	// UseTypeUI5App is a SAPUI5 repository app launched by a UIAD app entry.
	// Unlike the other targets it is not a TADIR object, so it cannot be
	// classified further; it is reported as a plain reference.
	UseTypeUI5App = "UI5_APP"
)

// uiadTarget names one SUI_TM_MM_APP column that may reference a launch target
// and the use type reported for it.
type uiadTarget struct {
	Column  string
	UseType string
}

// uiadTargetColumns lists the SUI_TM_MM_APP columns that carry a launch target,
// in the order they are reported. Resolution is by column rather than by
// APP_TYPE: a Web Dynpro app entry carries both WD_APPL_ID and TCODE, and a
// WebClient UI entry carries both WCF_TARGET_ID and TCODE, so keying off
// APP_TYPE would silently drop the second reference.
var uiadTargetColumns = []uiadTarget{
	{Column: "TCODE", UseType: UseTypeTransaction},
	{Column: "WD_APPL_ID", UseType: UseTypeWebDynproApp},
	{Column: "WCF_TARGET_ID", UseType: UseTypeWebClientTarget},
	{Column: "UI5_APP_ID", UseType: UseTypeUI5App},
}

// ObjectDependency is a single object referenced by the queried object.
type ObjectDependency struct {
	Name    string `json:"name"`
	UseType string `json:"use_type"`
}

// DependencyResult is returned by GetObjectDependencies.
type DependencyResult struct {
	ObjectType   string             `json:"object_type"`
	ObjectName   string             `json:"object_name"`
	Count        int                `json:"count"`
	Dependencies []ObjectDependency `json:"dependencies"`
	Warnings     []string           `json:"warnings,omitempty"`
}

const (
	// dependencyMaxDepthCeiling caps the BFS depth.
	dependencyMaxDepthCeiling = 10
	// seometarelMaxRows caps OO relationship lookups; well above any realistic
	// class hierarchy.
	seometarelMaxRows = 100
	// ddicMaxFieldRows caps DD03L batch queries.
	ddicMaxFieldRows = 2000
	// ddicInListMaxBytes is the maximum byte length of a SQL IN-list per batch.
	// SAP's data-preview endpoint misreads long IN-lists as an unclosed string
	// literal (HTTP 400 "text literal longer than 255 characters") because it
	// appends an internal INTO TABLE clause; 150 bytes keeps every batch safe.
	ddicInListMaxBytes = 150
)

// GetObjectDependencies finds the DDIC objects (tables, structures, types) and
// OO relationships (implemented interfaces, superclass) that an ABAP object
// references. It is the counterpart to WhereUsed.
//
// Supported objectType values: PROG, FUGR, FUNC, CLAS, INTF (flat lookup via
// D010TAB, populated by the activator; CLAS/INTF additionally consult
// SEOMETAREL), and TABL, DTEL, DOMA, TTYP (iterative BFS over the DDIC catalog
// tables up to maxDepth levels). maxResults caps the returned list (0 = no
// cap). maxDepth is clamped to [1, 10] (values below 1 become 1); it is
// ignored for the non-DDIC types. Callers choose their own default depth.
//
// UIAC (Fiori catalog) lists the UIAD app entries it contains, honouring
// maxResults; UIAD lists the launch target of a single app entry and ignores
// maxResults, since one app entry has at most four targets. Both read
// SUI_TM_MM_APP and both ignore maxDepth.
func (c *httpClient) GetObjectDependencies(ctx context.Context, objectType, objectName string, maxResults, maxDepth int) (*DependencyResult, error) {
	objectType = strings.ToUpper(objectType)

	switch objectType {
	case "PROG":
		deps, err := c.d010tabDeps(ctx, objectName, maxResults)
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, deps, nil), nil

	case "FUGR":
		deps, err := c.d010tabDeps(ctx, fugrPoolProgramName(objectName), maxResults)
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, deps, nil), nil

	case "FUNC":
		master, err := c.funcPoolProgramName(ctx, objectName)
		if err != nil {
			return nil, err
		}
		deps, err := c.d010tabDeps(ctx, master, maxResults)
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, deps, nil), nil

	case "CLAS":
		ddic, err := c.d010tabDeps(ctx, classPoolProgramName(objectName), maxResults)
		if err != nil {
			return nil, err
		}
		oo, err := c.ooDeps(ctx, objectName, []string{"1", "2"})
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, append(ddic, oo...), nil), nil

	case "INTF":
		ddic, err := c.d010tabDeps(ctx, intfPoolProgramName(objectName), maxResults)
		if err != nil {
			return nil, err
		}
		oo, err := c.ooDeps(ctx, objectName, []string{"0"})
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, append(ddic, oo...), nil), nil

	case "UIAC":
		deps, warns, err := c.uiacDeps(ctx, objectName, maxResults)
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, deps, warns), nil

	case "UIAD":
		deps, warns, err := c.uiadDeps(ctx, objectName)
		if err != nil {
			return nil, err
		}
		return newDependencyResult(objectType, objectName, deps, warns), nil

	case "TABL", "DTEL", "DOMA", "TTYP":
		if maxDepth < 1 {
			maxDepth = 1
		}
		if maxDepth > dependencyMaxDepthCeiling {
			maxDepth = dependencyMaxDepthCeiling
		}
		deps, warns := c.ddicChainDeps(ctx, objectName, objectType, maxDepth)
		if maxResults > 0 && len(deps) > maxResults {
			warns = append(warns, fmt.Sprintf("output truncated to %d entries (%d total)", maxResults, len(deps)))
			deps = deps[:maxResults]
		}
		return newDependencyResult(objectType, objectName, deps, warns), nil

	default:
		return nil, fmt.Errorf("unsupported object type %q: supported are PROG, FUGR, FUNC, CLAS, INTF, TABL, DTEL, DOMA, TTYP, UIAC, UIAD", objectType)
	}
}

func newDependencyResult(objectType, objectName string, deps []ObjectDependency, warnings []string) *DependencyResult {
	return &DependencyResult{
		ObjectType:   objectType,
		ObjectName:   objectName,
		Count:        len(deps),
		Dependencies: deps,
		Warnings:     warnings,
	}
}

// d010tabDeps returns the flat DDIC dependency set of a program-like object.
// D010TAB is populated by the ABAP activator at activation time, so a single
// MASTER lookup returns the complete set (including objects pulled in via
// INCLUDE) with no client-side recursion.
func (c *httpClient) d010tabDeps(ctx context.Context, master string, maxResults int) ([]ObjectDependency, error) {
	qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT TABNAME FROM D010TAB WHERE MASTER = '%s' ORDER BY TABNAME", EscapeValue(master)),
		maxResults)
	if err != nil {
		return nil, err
	}
	if qr == nil {
		return nil, nil
	}
	var names []string
	deps := make([]ObjectDependency, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 1 || row[0] == "" {
			continue
		}
		names = append(names, row[0])
		deps = append(deps, ObjectDependency{Name: row[0]})
	}
	if len(names) > 0 {
		classification := c.classifyDDICObjects(ctx, names)
		for i := range deps {
			deps[i].UseType = classification[deps[i].Name]
		}
	}
	return deps, nil
}

// uiacDeps lists the UIAD app entries a UIAC catalog contains. The catalog
// itself is not probed: SUI_TM_MM_CAT is absent on ECC systems, and an empty
// SUI_TM_MM_APP result is already the correct answer for a catalog with no
// app entries.
//
// RunQuery silently rewrites a non-positive maxResults to a 1000-row server
// cap (see RunQuery in query.go) — GetObjectDependencies documents maxResults
// == 0 as "no cap", so a catalog with more than 1000 app entries would
// otherwise be truncated with no indication to the caller. QueryResult's
// TotalRows carries the server-side total independently of how many rows
// were actually returned, so a mismatch between the two is reported as a
// warning naming both numbers.
func (c *httpClient) uiacDeps(ctx context.Context, catID string, maxResults int) ([]ObjectDependency, []string, error) {
	qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT APP_ID FROM SUI_TM_MM_APP WHERE CAT_ID = '%s' ORDER BY APP_ID", EscapeValue(catID)),
		maxResults)
	if err != nil {
		return nil, nil, err
	}
	if qr == nil {
		return nil, nil, nil
	}
	idx := queryColumnIndexes(qr, "APP_ID")
	deps := make([]ObjectDependency, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if v := queryCell(row, idx["APP_ID"]); v != "" {
			deps = append(deps, ObjectDependency{Name: v, UseType: UseTypeUIApp})
		}
	}
	var warnings []string
	if qr.TotalRows > len(qr.Rows) {
		warnings = append(warnings, fmt.Sprintf("output truncated to %d entries (%d total)", len(qr.Rows), qr.TotalRows))
	}
	return deps, warnings, nil
}

// uiadDeps resolves the launch target of a UIAD app entry. Every target column
// that is filled is reported; see uiadTargetColumns for why APP_TYPE is not
// used to select one. Columns are addressed by name because the data-preview
// endpoint may reorder or omit them. An app entry that launches no repository
// object (a URL app) yields no dependencies and one warning.
func (c *httpClient) uiadDeps(ctx context.Context, appID string) ([]ObjectDependency, []string, error) {
	names := make([]string, 0, len(uiadTargetColumns)+1)
	names = append(names, "APP_TYPE")
	for _, t := range uiadTargetColumns {
		names = append(names, t.Column)
	}
	qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT %s FROM SUI_TM_MM_APP WHERE APP_ID = '%s'",
			strings.Join(names, ", "), EscapeValue(appID)),
		1)
	if err != nil {
		return nil, nil, err
	}
	if qr == nil || len(qr.Rows) == 0 {
		return nil, []string{fmt.Sprintf("no SUI_TM_MM_APP entry for app id %q", appID)}, nil
	}
	idx := queryColumnIndexes(qr, names...)
	row := qr.Rows[0]
	deps := make([]ObjectDependency, 0, len(uiadTargetColumns))
	for _, t := range uiadTargetColumns {
		if v := queryCell(row, idx[t.Column]); v != "" {
			deps = append(deps, ObjectDependency{Name: v, UseType: t.UseType})
		}
	}
	if len(deps) == 0 {
		return nil, []string{fmt.Sprintf("app entry has no launch target (APP_TYPE %q)", queryCell(row, idx["APP_TYPE"]))}, nil
	}
	return deps, nil, nil
}

// classifyDDICObjects resolves the DDIC kind of each name via two queries:
// DD02L first (tables/structures/views, including SAP system objects like SYST
// that are absent from TADIR), then TADIR for the remainder (data elements,
// domains, table types). Query errors degrade gracefully: affected names stay
// UNKNOWN rather than failing the call.
func (c *httpClient) classifyDDICObjects(ctx context.Context, names []string) map[string]string {
	result := make(map[string]string, len(names))
	for _, n := range names {
		result[n] = UseTypeUnknown
	}

	if qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT TABNAME, TABCLASS FROM DD02L WHERE TABNAME IN (%s)", buildSQLInList(names)),
		len(names)); err == nil && qr != nil {
		for _, row := range qr.Rows {
			if len(row) >= 2 {
				result[row[0]] = tabclassToUseType(row[1])
			}
		}
	}

	var unknownNames []string
	for _, n := range names {
		if result[n] == UseTypeUnknown {
			unknownNames = append(unknownNames, n)
		}
	}
	if len(unknownNames) > 0 {
		if qr, err := c.RunQuery(ctx,
			fmt.Sprintf("SELECT OBJECT, OBJ_NAME FROM TADIR WHERE PGMID = 'R3TR' AND OBJ_NAME IN (%s)", buildSQLInList(unknownNames)),
			len(unknownNames)); err == nil && qr != nil {
			for _, row := range qr.Rows {
				if len(row) < 2 {
					continue
				}
				switch row[0] {
				case "DTEL":
					result[row[1]] = UseTypeDataElement
				case "DOMA":
					result[row[1]] = UseTypeDomain
				case "TTYP":
					result[row[1]] = UseTypeTableType
				case "VIEW":
					result[row[1]] = UseTypeView
				}
			}
		}
	}

	return result
}

// ooDeps returns the OO relationships of a class or interface from SEOMETAREL.
// relTypes selects the relationship kinds (CLAS: ["1","2"], INTF: ["0"]).
func (c *httpClient) ooDeps(ctx context.Context, clsName string, relTypes []string) ([]ObjectDependency, error) {
	qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT REFCLSNAME, RELTYPE FROM SEOMETAREL WHERE CLSNAME = '%s' AND RELTYPE IN (%s) ORDER BY RELTYPE, REFCLSNAME",
			EscapeValue(clsName), buildSQLInList(relTypes)),
		seometarelMaxRows)
	if err != nil {
		return nil, err
	}
	if qr == nil {
		return nil, nil
	}
	deps := make([]ObjectDependency, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 2 || row[0] == "" {
			continue
		}
		deps = append(deps, ObjectDependency{Name: row[0], UseType: ooRelTypeToUseType(row[1])})
	}
	return deps, nil
}

// ooRelTypeToUseType maps SEOMETAREL.RELTYPE to a use type. "0"/"1" (interface
// extension/implementation) collapse to INTERFACE; "2" is the superclass.
func ooRelTypeToUseType(relType string) string {
	switch relType {
	case "0", "1":
		return UseTypeInterface
	case "2":
		return UseTypeSuperclass
	default:
		return UseTypeUnknown
	}
}

// fugrPoolProgramName builds the D010TAB MASTER key for a function group:
// SAPL<name>, with namespace splicing for /NS/<local> -> /NS/SAPL<local>.
func fugrPoolProgramName(fugrName string) string {
	if len(fugrName) > 0 && fugrName[0] == '/' {
		if idx := strings.Index(fugrName[1:], "/"); idx >= 0 {
			ns := fugrName[:idx+2]    // "/NS/"
			local := fugrName[idx+2:] // "LOCALNAME"
			return ns + "SAPL" + local
		}
	}
	return "SAPL" + fugrName
}

// classPoolProgramName builds the D010TAB MASTER key for a class:
// <name> padded with '=' to 30 chars + "CP".
func classPoolProgramName(className string) string {
	const padLen = 30
	if len(className) >= padLen {
		return className + "CP"
	}
	return className + strings.Repeat("=", padLen-len(className)) + "CP"
}

// intfPoolProgramName builds the D010TAB MASTER key for an interface:
// <name> padded with '=' to 30 chars + "IP".
func intfPoolProgramName(intfName string) string {
	const padLen = 30
	if len(intfName) >= padLen {
		return intfName + "IP"
	}
	return intfName + strings.Repeat("=", padLen-len(intfName)) + "IP"
}

// funcPoolProgramName resolves the D010TAB MASTER key for a function module by
// looking up TFDIR.PNAME — the function pool program SAP generated for its group.
func (c *httpClient) funcPoolProgramName(ctx context.Context, funcName string) (string, error) {
	qr, err := c.RunQuery(ctx,
		fmt.Sprintf("SELECT PNAME FROM TFDIR WHERE FUNCNAME = '%s'", EscapeValue(funcName)),
		1)
	if err != nil {
		return "", fmt.Errorf("looking up function module %q in TFDIR: %w", funcName, err)
	}
	if qr == nil || len(qr.Rows) == 0 || len(qr.Rows[0]) == 0 || qr.Rows[0][0] == "" {
		return "", fmt.Errorf("function module %q not found in TFDIR", funcName)
	}
	return qr.Rows[0][0], nil
}

// tabclassToUseType maps DD02L.TABCLASS to a use type. Unknown classes map to
// UNKNOWN rather than TABLE so new SAP kinds don't masquerade as tables.
func tabclassToUseType(tabclass string) string {
	switch tabclass {
	case "TRANSP":
		return UseTypeTable
	case "INTTAB":
		return UseTypeStructure
	case "CLUSTER", "POOL":
		return UseTypeTable
	case "VIEW":
		return UseTypeView
	default:
		return UseTypeUnknown
	}
}

func buildSQLInList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + EscapeValue(n) + "'"
	}
	return strings.Join(quoted, ",")
}

// chunkNames splits names into batches whose buildSQLInList output stays within
// maxBytes, avoiding SAP data-preview parser errors on long IN-lists.
func chunkNames(names []string, maxBytes int) [][]string {
	var chunks [][]string
	var cur []string
	curLen := 0
	for _, n := range names {
		entryLen := len(n) + 3 // 'name', = name + 2 quotes + 1 comma
		if len(cur) > 0 && curLen+entryLen > maxBytes {
			chunks = append(chunks, cur)
			cur = nil
			curLen = 0
		}
		cur = append(cur, n)
		curLen += entryLen
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// ddicQueryTabl queries DD03L for TABL entries: ROLLNAME -> DTEL, CHECKTABLE -> TABL.
func (c *httpClient) ddicQueryTabl(ctx context.Context, names []string, addDep func(string, string, string), warnings *[]string) {
	for _, chunk := range chunkNames(names, ddicInListMaxBytes) {
		qr, err := c.RunQuery(ctx,
			fmt.Sprintf("SELECT ROLLNAME, CHECKTABLE FROM DD03L WHERE TABNAME IN (%s)", buildSQLInList(chunk)),
			ddicMaxFieldRows)
		if err != nil {
			*warnings = append(*warnings, "DD03L query failed: "+err.Error())
			continue
		}
		if qr == nil {
			continue
		}
		for _, row := range qr.Rows {
			if len(row) < 2 {
				continue
			}
			if row[0] != "" {
				addDep(row[0], "DTEL", UseTypeDataElement)
			}
			if row[1] != "" {
				addDep(row[1], "TABL", UseTypeTable)
			}
		}
		if len(qr.Rows) >= ddicMaxFieldRows {
			*warnings = append(*warnings, fmt.Sprintf("DD03L result capped at %d rows; some field dependencies may be missing", ddicMaxFieldRows))
		}
	}
}

// ddicQueryDtel queries DD04L for DTEL entries: DOMNAME -> DOMA.
func (c *httpClient) ddicQueryDtel(ctx context.Context, names []string, addDep func(string, string, string), warnings *[]string) {
	for _, chunk := range chunkNames(names, ddicInListMaxBytes) {
		qr, err := c.RunQuery(ctx,
			fmt.Sprintf("SELECT DOMNAME FROM DD04L WHERE ROLLNAME IN (%s)", buildSQLInList(chunk)),
			len(chunk))
		if err != nil {
			*warnings = append(*warnings, "DD04L query failed: "+err.Error())
			continue
		}
		if qr == nil {
			continue
		}
		for _, row := range qr.Rows {
			if len(row) < 1 {
				continue
			}
			addDep(row[0], "DOMA", UseTypeDomain)
		}
	}
}

// ddicQueryDoma queries DD01L for DOMA entries: ENTITYTAB -> TABL.
func (c *httpClient) ddicQueryDoma(ctx context.Context, names []string, addDep func(string, string, string), warnings *[]string) {
	for _, chunk := range chunkNames(names, ddicInListMaxBytes) {
		qr, err := c.RunQuery(ctx,
			fmt.Sprintf("SELECT ENTITYTAB FROM DD01L WHERE DOMNAME IN (%s)", buildSQLInList(chunk)),
			len(chunk))
		if err != nil {
			*warnings = append(*warnings, "DD01L query failed: "+err.Error())
			continue
		}
		if qr == nil {
			continue
		}
		for _, row := range qr.Rows {
			if len(row) < 1 {
				continue
			}
			addDep(row[0], "TABL", UseTypeTable)
		}
	}
}

// ddicQueryTtyp queries DD40L for TTYP entries: ROWKIND='E' -> DTEL,
// ROWKIND='S' -> TABLE/STRUCTURE (classified via DD02L). ROWKIND=” is a
// built-in scalar with no further traversal.
func (c *httpClient) ddicQueryTtyp(ctx context.Context, names []string, addDep func(string, string, string), warnings *[]string) {
	var rowKindS []string
	for _, chunk := range chunkNames(names, ddicInListMaxBytes) {
		qr, err := c.RunQuery(ctx,
			fmt.Sprintf("SELECT ROWTYPE, ROWKIND FROM DD40L WHERE TYPENAME IN (%s)", buildSQLInList(chunk)),
			len(chunk))
		if err != nil {
			*warnings = append(*warnings, "DD40L query failed: "+err.Error())
			continue
		}
		if qr == nil {
			continue
		}
		for _, row := range qr.Rows {
			if len(row) < 2 || row[0] == "" {
				continue
			}
			switch row[1] {
			case "E":
				addDep(row[0], "DTEL", UseTypeDataElement)
			case "S":
				rowKindS = append(rowKindS, row[0])
			}
		}
	}
	for _, clsChunk := range chunkNames(rowKindS, ddicInListMaxBytes) {
		cls := c.classifyDDICObjects(ctx, clsChunk)
		for _, n := range clsChunk {
			addDep(n, "TABL", cls[n])
		}
	}
}

// ddicChainDeps traverses the DDIC type chain from a single object (TABL, DTEL,
// DOMA, TTYP) by iterative BFS, returning a flat deduplicated dependency list
// plus any non-fatal query warnings. Iterative BFS (with a visited set) avoids
// stack overflow on cyclic chains such as DOMA->ENTITYTAB->TABL->ROLLNAME->DTEL->DOMA.
func (c *httpClient) ddicChainDeps(ctx context.Context, name, objType string, maxDepth int) ([]ObjectDependency, []string) {
	type queueEntry struct {
		name    string
		objType string
	}

	visited := map[string]bool{objType + "|" + name: true}
	var deps []ObjectDependency
	var warnings []string

	current := []queueEntry{{name: name, objType: objType}}

	for depth := 0; depth < maxDepth && len(current) > 0; depth++ {
		var next []queueEntry

		addDep := func(depName, depType, useType string) {
			if depName == "" {
				return
			}
			k := depType + "|" + depName
			if visited[k] {
				return
			}
			visited[k] = true
			deps = append(deps, ObjectDependency{Name: depName, UseType: useType})
			next = append(next, queueEntry{name: depName, objType: depType})
		}

		typeGroups := map[string][]string{}
		for _, e := range current {
			typeGroups[e.objType] = append(typeGroups[e.objType], e.name)
		}

		if tabls := typeGroups["TABL"]; len(tabls) > 0 {
			c.ddicQueryTabl(ctx, tabls, addDep, &warnings)
		}
		if dtels := typeGroups["DTEL"]; len(dtels) > 0 {
			c.ddicQueryDtel(ctx, dtels, addDep, &warnings)
		}
		if domas := typeGroups["DOMA"]; len(domas) > 0 {
			c.ddicQueryDoma(ctx, domas, addDep, &warnings)
		}
		if ttyps := typeGroups["TTYP"]; len(ttyps) > 0 {
			c.ddicQueryTtyp(ctx, ttyps, addDep, &warnings)
		}

		current = next
	}

	return deps, warnings
}
