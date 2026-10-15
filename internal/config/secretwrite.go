// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// SecretNames lists the names set in secrets.env, never their values.
func SecretNames(dir string) ([]string, error) {
	s, issues := readSecrets(dirSource(dir))
	if len(issues) > 0 {
		return nil, errors.New(issues[0].Message)
	}
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	return names, nil
}

// SetSecret adds or replaces one secret in secrets.env and keeps every other
// line, comments included. An empty value removes the secret.
func SetSecret(dir, name, value string) error {
	if !secretName.MatchString(name) {
		return fmt.Errorf("%q is not a valid name: use letters, digits and _, not starting with a digit", name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("a secret cannot contain line breaks")
	}
	data, err := os.ReadFile(SecretsPath(dir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(data) > maxSecretsSize {
		return fmt.Errorf("%s is larger than %d KiB", SecretsFile, maxSecretsSize>>10)
	}
	var out []string
	replaced := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == name && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			if value != "" && !replaced {
				out = append(out, secretLine(name, value))
			}
			replaced = true
			continue
		}
		if line != "" || len(out) > 0 {
			out = append(out, line)
		}
	}
	if !replaced && value != "" {
		out = append(out, secretLine(name, value))
	}
	if !replaced && value == "" {
		return fmt.Errorf("%s is not set", name)
	}
	return writeFileAtomic(SecretsPath(dir), []byte(strings.Join(out, "\n")+"\n"))
}

// secretLine quotes the value, so quotes or spaces at its ends survive reading back.
func secretLine(name, value string) string { return name + `="` + value + `"` }
