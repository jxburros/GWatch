package api

import "maps"

func maskSecret(v string) (string, error) {
	if v != "" {
		return passwordMask, nil
	}
	return "", nil
}
func blankSecret(string) (string, error) { return "", nil }
func restoreSecretMap(dst *map[string]string, old map[string]string) {
	*dst = maps.Clone(*dst)
	for k, v := range *dst {
		if v == passwordMask {
			(*dst)[k] = old[k]
		}
	}
}
