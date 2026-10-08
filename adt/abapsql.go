package adt

import (
	"fmt"
	"regexp"
	"strings"
)

// identifierRe matches valid ABAP table/column names.
// Allows alphanumerics, forward slash (namespaces like /ABC/TABLE),
// underscore, and hash (e.g. #MIN).
var identifierRe = regexp.MustCompile(`^[A-Za-z0-9/_#]+$`)

// validateIdentifier checks that a table or column name is safe for SQL interpolation.
func validateIdentifier(name string) error {
	if name == "" {
		return fmt.Errorf("identifier must not be empty")
	}
	if !identifierRe.MatchString(name) {
		return fmt.Errorf("invalid identifier %q: must match %s", name, identifierRe.String())
	}
	return nil
}

// FilterNonMandtKeys returns key fields excluding MANDT.
// The comparison is case-insensitive because SAP metadata may use varying cases.
func FilterNonMandtKeys(keys []string) []string {
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		if !strings.EqualFold(k, "MANDT") {
			result = append(result, k)
		}
	}
	return result
}

// EscapeValue escapes single quotes in a SQL string value by doubling them.
// This is sufficient for ABAP Open SQL where single-quote doubling is the only
// escape mechanism. NOT safe for general SQL engines (backslash escapes, etc.).
func EscapeValue(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

// buildPaginationWhere generates an OR-chain WHERE clause for key-based pagination.
//
// ABAP Open SQL does not support tuple comparison like (K1, K2) > ('v1', 'v2'),
// so we expand into an equivalent OR-chain:
//
//	1 key:  K1 > 'v1'
//	2 keys: K1 > 'v1' OR ( K1 = 'v1' AND K2 > 'v2' )
//	3 keys: K1 > 'v1' OR ( K1 = 'v1' AND K2 > 'v2' ) OR ( K1 = 'v1' AND K2 = 'v2' AND K3 > 'v3' )
//
// Returns an empty string if keys is empty or if keys and lastValues have different lengths.
func buildPaginationWhere(keys, lastValues []string) string {
	return buildPaginationWhereTyped(keys, lastValues, nil)
}

// ddicTypeTims is the DDIC data type of time fields (ABAP type T).
const ddicTypeTims = "TIMS"

// keyOperand renders a key field as the left side of a pagination comparison.
// The data preview rejects the literal '240000' for a TIMS field ("not a valid
// value for T(6,0)") although the database stores that value, so TIMS keys are
// compared as text. The text sorts like the time, and ORDER BY keeps using the
// plain field.
func keyOperand(key string, keyTypes map[string]string) string {
	if strings.EqualFold(keyTypes[key], ddicTypeTims) {
		return "CAST( " + key + " AS CHAR( 6 ) )"
	}
	return key
}

// buildPaginationWhereTyped is buildPaginationWhere with the DDIC data type of
// each key field (key name to type such as "TIMS").
func buildPaginationWhereTyped(keys, lastValues []string, keyTypes map[string]string) string {
	if len(keys) == 0 || len(keys) != len(lastValues) {
		return ""
	}

	var terms []string
	for i := range keys {
		var parts []string
		// All preceding keys are equal.
		for j := 0; j < i; j++ {
			parts = append(parts, fmt.Sprintf("%s = '%s'", keyOperand(keys[j], keyTypes), EscapeValue(lastValues[j])))
		}
		// The i-th key is strictly greater.
		parts = append(parts, fmt.Sprintf("%s > '%s'", keyOperand(keys[i], keyTypes), EscapeValue(lastValues[i])))

		if len(parts) == 1 {
			terms = append(terms, parts[0])
		} else {
			terms = append(terms, "( "+strings.Join(parts, " AND ")+" )")
		}
	}
	return strings.Join(terms, " OR ")
}

// BuildExportSQL generates the SELECT statement for exporting a customizing table.
//
// allKeys: all key fields including MANDT (used for ORDER BY).
// paginateKeys: non-MANDT keys used for pagination WHERE clause. Must NOT include MANDT.
//
// lastValues: values from the last row for pagination. Must match paginateKeys length.
//
//	Pass nil for the first page.
func BuildExportSQL(table string, allKeys []string, paginateKeys []string, lastValues []string) (string, error) {
	return BuildExportSQLTyped(table, allKeys, paginateKeys, lastValues, nil)
}

// BuildExportSQLTyped is BuildExportSQL with the DDIC data type of the key
// fields (key name to type such as "TIMS"). keyTypes may be nil, and the type
// match is case-insensitive. Only keys of type TIMS are treated specially: in
// the pagination WHERE clause they are compared as CAST( f AS CHAR( 6 ) )
// because the data preview rejects the stored value 240000 as a time literal.
// The ORDER BY clause is unchanged.
func BuildExportSQLTyped(table string, allKeys []string, paginateKeys []string, lastValues []string, keyTypes map[string]string) (string, error) {
	if err := validateIdentifier(table); err != nil {
		return "", fmt.Errorf("invalid table name: %w", err)
	}
	for _, k := range allKeys {
		if err := validateIdentifier(k); err != nil {
			return "", fmt.Errorf("invalid key column: %w", err)
		}
	}
	for _, k := range paginateKeys {
		if err := validateIdentifier(k); err != nil {
			return "", fmt.Errorf("invalid pagination key: %w", err)
		}
	}

	var sb strings.Builder
	sb.WriteString("SELECT * FROM ")
	sb.WriteString(table)

	if len(lastValues) > 0 && len(paginateKeys) > 0 {
		where := buildPaginationWhereTyped(paginateKeys, lastValues, keyTypes)
		if where != "" {
			sb.WriteString(" WHERE ")
			sb.WriteString(where)
		}
	}

	if len(allKeys) > 0 {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(strings.Join(allKeys, ", "))
	}

	return sb.String(), nil
}
