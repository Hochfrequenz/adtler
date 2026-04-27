package adt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// FieldInfo describes a single field in a DDIC table or structure.
type FieldInfo struct {
	Name        string `json:"name"`
	Position    int    `json:"position"`
	IsKey       bool   `json:"is_key"`
	DataType    string `json:"data_type"` // CHAR, NUMC, DATS, etc.
	Length      int    `json:"length"`
	Decimals    int    `json:"decimals"`
	Domain      string `json:"domain"`
	DataElement string `json:"data_element"`
}

// GetTableFields returns the field definitions of a DDIC table or structure.
// Returns a typed *TableNotFoundError when DD03L has no rows for the supplied
// name (no real DDIC table can have zero fields, so an empty result always
// means the table doesn't exist). Callers can use errors.As to distinguish
// "not found" from other failures.
func (c *httpClient) GetTableFields(ctx context.Context, tableName string) ([]FieldInfo, error) {
	table := strings.ToUpper(strings.TrimSpace(tableName))
	if table == "" {
		return nil, fmt.Errorf("GetTableFields: table name must not be empty")
	}

	sql := fmt.Sprintf(
		"SELECT FIELDNAME, POSITION, KEYFLAG, DATATYPE, LENG, DECIMALS, DOMNAME, ROLLNAME "+
			"FROM DD03L WHERE TABNAME = '%s' ORDER BY POSITION",
		strings.ReplaceAll(table, "'", "''"),
	)
	result, err := c.RunQuery(ctx, sql, 500)
	if err != nil {
		return nil, fmt.Errorf("GetTableFields: %w", err)
	}

	fields := make([]FieldInfo, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 8 {
			continue
		}
		name := strings.TrimSpace(row[0])
		// Skip pseudo-fields (.INCLUDE, .APPEND, etc.)
		if strings.HasPrefix(name, ".") {
			continue
		}
		pos, _ := strconv.Atoi(strings.TrimSpace(row[1]))
		length, _ := strconv.Atoi(strings.TrimSpace(row[4]))
		decimals, _ := strconv.Atoi(strings.TrimSpace(row[5]))

		fields = append(fields, FieldInfo{
			Name:        name,
			Position:    pos,
			IsKey:       strings.TrimSpace(row[2]) == "X",
			DataType:    strings.TrimSpace(row[3]),
			Length:      length,
			Decimals:    decimals,
			Domain:      strings.TrimSpace(row[6]),
			DataElement: strings.TrimSpace(row[7]),
		})
	}
	if len(fields) == 0 {
		return nil, &TableNotFoundError{TableName: table}
	}
	return fields, nil
}

// TableNotFoundError is returned by GetTableFields when DD03L has no rows
// for the requested table name. This is the framework's signal that the
// table doesn't exist — no real DDIC table can have zero fields.
type TableNotFoundError struct {
	TableName string
}

func (e *TableNotFoundError) Error() string {
	return fmt.Sprintf("GetTableFields: DDIC table %q does not exist (DD03L has no rows for it)", e.TableName)
}
