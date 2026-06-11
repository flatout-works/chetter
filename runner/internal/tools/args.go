package tools

import "fmt"

func getString(args map[string]any, key string) (string, error) {
	v, ok := args[key].(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", key)
	}
	return v, nil
}

func requireString(args map[string]any, key string) (string, error) {
	v, err := getString(args, key)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("missing %s", key)
	}
	return v, nil
}

func getOptString(args map[string]any, key string, defaultVal string) string {
	v, ok := args[key].(string)
	if !ok {
		return defaultVal
	}
	return v
}

func getOptFloat64(args map[string]any, key string, defaultVal float64) float64 {
	v, ok := args[key].(float64)
	if !ok {
		return defaultVal
	}
	return v
}

func getOptBool(args map[string]any, key string, defaultVal bool) bool {
	v, ok := args[key].(bool)
	if !ok {
		return defaultVal
	}
	return v
}

func getOptStringMap(args map[string]any, key string) map[string]any {
	v, ok := args[key].(map[string]any)
	if !ok {
		return nil
	}
	return v
}

func getOptStringSlice(args map[string]any, key string) []any {
	v, ok := args[key].([]any)
	if !ok {
		return nil
	}
	return v
}
