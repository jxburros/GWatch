package model

import "maps"

// SecretFields is the single list used by API redaction and storage encryption.
func (c *CheckConfig) SecretFields() []*string {
	return []*string{&c.MetricsToken, &c.SNMPCommunity, &c.SNMPAuthPass, &c.SNMPPrivPass, &c.Body}
}

// TransformSecrets copies maps before touching them, so masking a value copy
// never mutates the engine's live credentials.
func (c *CheckConfig) TransformSecrets(f func(string) (string, error)) error {
	return transformSecrets(c.SecretFields(), []*map[string]string{&c.Headers, &c.Env}, f)
}

func (a *Action) TransformSecrets(f func(string) (string, error)) error {
	return transformSecrets([]*string{&a.Token, &a.UserKey, &a.WebhookURL, &a.URL, &a.Body}, []*map[string]string{&a.Headers}, f)
}

func transformSecrets(fields []*string, dictionaries []*map[string]string, f func(string) (string, error)) error {
	for _, p := range fields {
		v, err := f(*p)
		if err != nil {
			return err
		}
		*p = v
	}
	for _, p := range dictionaries {
		*p = maps.Clone(*p)
		for k, old := range *p {
			v, err := f(old)
			if err != nil {
				return err
			}
			(*p)[k] = v
		}
	}
	return nil
}
