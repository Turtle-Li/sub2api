package repository

import "fmt"

func appendPaidFallbackWhereCondition(conditions []string, args []any, value *bool, alias string) ([]string, []any) {
	if value == nil {
		return conditions, args
	}
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return append(conditions, fmt.Sprintf("%spaid_fallback = $%d", prefix, len(args)+1)), append(args, *value)
}

func appendPaidFallbackQueryFilter(query string, args []any, values []*bool, alias string) (string, []any) {
	if len(values) == 0 || values[0] == nil {
		return query, args
	}
	conditions, args := appendPaidFallbackWhereCondition(nil, args, values[0], alias)
	return query + " AND " + conditions[0], args
}
