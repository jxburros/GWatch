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

// SecretFields lists the string fields of an action that hold, or may hold, a
// credential: tokens and keys outright, and the URLs and bodies that commonly
// carry one (a Slack or Discord webhook URL is itself the secret). Storage
// seals exactly these, the API masks exactly these, and a save that sends a
// mask back restores exactly these, so the three can never disagree.
func (a *Action) SecretFields() []*string {
	return []*string{&a.Token, &a.UserKey, &a.WebhookURL, &a.URL, &a.Body, &a.RoutingKey}
}

func (a *Action) TransformSecrets(f func(string) (string, error)) error {
	return transformSecrets(a.SecretFields(), []*map[string]string{&a.Headers}, f)
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
