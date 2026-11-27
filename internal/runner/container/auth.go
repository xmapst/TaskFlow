package container

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/registry"
)

const (
	// dockerHub is the registry host a bare image name resolves to.
	dockerHub = "docker.io"
	// dockerHubKey is how `docker login` stores Docker Hub credentials: under
	// the full URL, not the host name.
	dockerHubKey = "https://index.docker.io/v1/"
)

// dockerConfig is the part of ~/.docker/config.json needed to authenticate a
// pull.
type dockerConfig struct {
	Auths       map[string]dockerAuth `json:"auths"`
	CredsStore  string                `json:"credsStore"`
	CredHelpers map[string]string     `json:"credHelpers"`
}

type dockerAuth struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
	RegistryToken string `json:"registrytoken"`
}

// dockerCredential is the answer of a credential helper's `get` command.
type dockerCredential struct {
	ServerURL string `json:"ServerURL"`
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
}

// registryAuth resolves the credentials for image and returns them as the
// base64 header the daemon expects, together with a description of where they
// came from for the step log.
//
// The Docker daemon keeps no credentials of its own: without this header a pull
// is anonymous, so private images need the credentials the `docker` CLI would
// have sent. They come from the same places the CLI uses: DOCKER_AUTH_CONFIG,
// the file under DOCKER_CONFIG (default ~/.docker/config.json), its
// credHelpers/credsStore helpers, or `docker login`.
func registryAuth(ctx context.Context, image string) (header, source string, err error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		// The pull reports a malformed reference far better than we can.
		return "", "", nil
	}
	host := normalizeRegistry(reference.Domain(named))
	cfg, err := loadDockerConfig()
	if err != nil {
		return "", "", err
	}
	auth, source, ok := cfg.lookup(ctx, host)
	if !ok {
		return "", "", nil
	}
	auth.ServerAddress = cfg.keyFor(host)
	raw, err := json.Marshal(auth)
	if err != nil {
		return "", "", err
	}
	return base64.URLEncoding.EncodeToString(raw), source, nil
}

func loadDockerConfig() (*dockerConfig, error) {
	// DOCKER_AUTH_CONFIG replaces the config file entirely, as in the CLI; it is
	// how a service gets credentials without a home directory.
	if raw := os.Getenv("DOCKER_AUTH_CONFIG"); raw != "" {
		cfg := new(dockerConfig)
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return nil, fmt.Errorf("parse DOCKER_AUTH_CONFIG: %w", err)
		}
		return cfg, nil
	}
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return &dockerConfig{}, nil
		}
		dir = filepath.Join(home, ".docker")
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &dockerConfig{}, nil
		}
		return nil, fmt.Errorf("read docker config: %w", err)
	}
	cfg := new(dockerConfig)
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse docker config %s: %w", path, err)
	}
	return cfg, nil
}

// lookup finds the credentials for a normalized registry host. A helper
// configured for that registry wins over the default helper, which wins over an
// entry in the file; a helper that has no credentials falls back to the file.
func (c *dockerConfig) lookup(ctx context.Context, host string) (registry.AuthConfig, string, bool) {
	for _, helper := range []struct{ name, from string }{
		{c.helperFor(host), "credential helper"},
		{c.CredsStore, "credential store"},
	} {
		if helper.name == "" {
			continue
		}
		if auth, ok := c.helperAuth(ctx, helper.name, c.keyFor(host)); ok {
			return auth, helper.from + " " + helper.name, true
		}
	}
	for key, entry := range c.Auths {
		if normalizeRegistry(key) == host {
			return entry.authConfig(), "docker config", true
		}
	}
	return registry.AuthConfig{}, "", false
}

func (c *dockerConfig) helperFor(host string) string {
	for key, helper := range c.CredHelpers {
		if normalizeRegistry(key) == host {
			return helper
		}
	}
	return ""
}

// keyFor returns the registry address as the config or the helper names it, so
// credentials stored under a URL form are still found.
func (c *dockerConfig) keyFor(host string) string {
	for key := range c.CredHelpers {
		if normalizeRegistry(key) == host {
			return key
		}
	}
	for key := range c.Auths {
		if normalizeRegistry(key) == host {
			return key
		}
	}
	if host == dockerHub {
		return dockerHubKey
	}
	return host
}

// helperAuth runs `docker-credential-<helper> get`, the CLI's credential helper
// protocol. A missing helper or absent entry is not an error: the caller falls
// back to the config file.
func (c *dockerConfig) helperAuth(ctx context.Context, helper, server string) (registry.AuthConfig, bool) {
	cmd := exec.CommandContext(ctx, "docker-credential-"+helper, "get")
	cmd.Stdin = strings.NewReader(server + "\n")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return registry.AuthConfig{}, false
	}
	var cred dockerCredential
	if err := json.Unmarshal(out.Bytes(), &cred); err != nil {
		return registry.AuthConfig{}, false
	}
	auth := registry.AuthConfig{ServerAddress: cred.ServerURL}
	// Several helpers use "<token>" to mean the secret is a registry token.
	if cred.Username == "<token>" {
		auth.IdentityToken = cred.Secret
	} else {
		auth.Username, auth.Password = cred.Username, cred.Secret
	}
	return auth, true
}

func (a dockerAuth) authConfig() registry.AuthConfig {
	auth := registry.AuthConfig{
		Username:      a.Username,
		Password:      a.Password,
		IdentityToken: a.IdentityToken,
		RegistryToken: a.RegistryToken,
	}
	// Older configs (and every `docker login` before credential stores) keep
	// "user:password" base64-encoded in auth.
	if auth.Username == "" && a.Auth != "" {
		if user, pass, ok := decodeAuth(a.Auth); ok {
			auth.Username, auth.Password = user, pass
		}
	}
	return auth
}

func decodeAuth(s string) (user, pass string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(raw), ":")
	if !ok {
		// "user" alone is not a credential; treat it as a broken entry.
		return "", "", false
	}
	return user, pass, true
}

// normalizeRegistry reduces a registry host or config key to a comparable host:
// the case, scheme, path and the several Docker Hub aliases are folded away.
func normalizeRegistry(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	switch s {
	case "index.docker.io", "registry-1.docker.io", dockerHub:
		return dockerHub
	}
	return s
}
