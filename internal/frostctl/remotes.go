package frostctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Remote struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	TokenConfigured bool   `json:"token_configured"`
	Credential      string `json:"credential,omitempty"`
	Store           string `json:"store,omitempty"`
}
type credential struct {
	Origin string `json:"origin"`
	Token  string `json:"token"`
}
type remoteStore struct {
	dir     string
	Entries map[string]Remote `json:"remotes"`
}

func configDir() (string, error) {
	if value := os.Getenv("FROSTCTL_CONFIG_DIR"); value != "" {
		return filepath.Abs(value)
	}
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "frostctl"), err
}
func canonicalURL(value string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", errors.New("remote URL must be HTTPS without credentials, query, or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", errors.New("invalid HTTPS host")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	origin := "https://" + host + ":" + port
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.TrimRight(u.String(), "/"), origin, nil
}
func withRemotes(ctx context.Context, run func(*remoteStore) (any, error)) (any, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create remote configuration directory")
	}
	if err = privatePath(dir); err != nil {
		return nil, err
	}
	lock, err := lockConfig(ctx, filepath.Join(dir, "config.lock"))
	if err != nil {
		return nil, err
	}
	defer lock()
	store := &remoteStore{dir: dir, Entries: map[string]Remote{}}
	path := filepath.Join(dir, "remotes.json")
	if _, err := os.Lstat(path); err == nil {
		if err = privatePath(path); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, errors.New("cannot read remote configuration")
		}
		if err = json.Unmarshal(data, store); err != nil {
			return nil, errors.New("invalid remote configuration")
		}
		if store.Entries == nil {
			store.Entries = map[string]Remote{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return run(store)
}
func privateWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".frostctl-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = privatePath(f.Name()); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(f.Name(), path)
}
func (s *remoteStore) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return privateWrite(filepath.Join(s.dir, "remotes.json"), append(data, '\n'))
}
func validRemoteName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func visibleRemote(r Remote) Remote { r.Credential = ""; r.Store = ""; return r }
func (s *remoteStore) removeCredential(ctx context.Context, r Remote) error {
	if r.Credential == "" {
		return nil
	}
	if !validRemoteName(r.Credential) {
		return errors.New("invalid credential reference")
	}
	if r.Store == "system" {
		return deleteSystemCredential(ctx, r.Credential)
	}
	err := os.Remove(filepath.Join(s.dir, r.Credential+".secret"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *remoteStore) token(ctx context.Context, r Remote) (string, error) {
	if !r.TokenConfigured || !validRemoteName(r.Credential) {
		return "", errors.New("remote token is not configured")
	}
	var data []byte
	var err error
	if r.Store == "system" {
		data, err = readSystemCredential(ctx, r.Credential)
	} else if r.Store == "file" {
		path := filepath.Join(s.dir, r.Credential+".secret")
		if err = privatePath(path); err == nil {
			data, err = os.ReadFile(path)
		}
	} else {
		return "", errors.New("invalid credential store")
	}
	if err != nil {
		return "", errors.New("saved remote credential is unavailable")
	}
	var c credential
	if json.Unmarshal(data, &c) != nil {
		return "", errors.New("invalid saved credential")
	}
	_, origin, err := canonicalURL(r.URL)
	if err != nil || c.Origin != origin {
		return "", errors.New("credential origin does not match the remote URL; set the token again")
	}
	return c.Token, nil
}
func readSecret(in io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(in, 4097))
	if err != nil || len(data) > 4096 {
		return "", errors.New("cannot read secret or secret exceeds 4096 bytes")
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("secret must contain one nonempty line")
	}
	return value, nil
}
func remoteCommand(ctx context.Context, inv Invocation, in io.Reader) (any, error) {
	return withRemotes(ctx, func(s *remoteStore) (any, error) {
		if inv.Command.Name == "remote list" {
			out := []Remote{}
			for _, r := range s.Entries {
				out = append(out, visibleRemote(r))
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
			return out, nil
		}
		name := inv.Args[0]
		if !validRemoteName(name) {
			return nil, errors.New("remote name must contain only letters, digits, dash, or underscore")
		}
		r, exists := s.Entries[name]
		if inv.Command.Name == "remote add" {
			if exists {
				return nil, errors.New("remote already exists")
			}
			normalized, _, err := canonicalURL(inv.Value("url"))
			if err != nil {
				return nil, err
			}
			r = Remote{Name: name, URL: normalized}
			s.Entries[name] = r
			if err = s.save(); err != nil {
				return nil, err
			}
			return visibleRemote(r), nil
		}
		if !exists {
			return nil, errors.New("remote does not exist")
		}
		switch inv.Command.Name {
		case "remote show":
			return visibleRemote(r), nil
		case "remote update":
			normalized, origin, err := canonicalURL(inv.Value("url"))
			if err != nil {
				return nil, err
			}
			_, previousOrigin, _ := canonicalURL(r.URL)
			old := r
			r.URL = normalized
			if origin != previousOrigin {
				r.TokenConfigured = false
				r.Credential = ""
				r.Store = ""
			}
			s.Entries[name] = r
			if err = s.save(); err != nil {
				return nil, err
			}
			if origin != previousOrigin {
				if err = s.removeCredential(ctx, old); err != nil {
					return nil, errors.New("URL updated and token unbound; old credential cleanup failed")
				}
			}
		case "remote token set":
			token, err := readSecret(in)
			if err != nil {
				return nil, err
			}
			for _, c := range token {
				if c < 33 || c > 126 {
					return nil, errors.New("management token must be printable ASCII without spaces")
				}
			}
			_, origin, err := canonicalURL(r.URL)
			if err != nil {
				return nil, err
			}
			data, _ := json.Marshal(credential{Origin: origin, Token: token})
			id := make([]byte, 16)
			if _, err = rand.Read(id); err != nil {
				return nil, err
			}
			old := r
			r.Credential = hex.EncodeToString(id)
			r.Store = "system"
			credentialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = writeSystemCredential(credentialCtx, r.Credential, data)
			cancel()
			if err != nil {
				r.Store = "file"
				err = privateWrite(filepath.Join(s.dir, r.Credential+".secret"), data)
			}
			if err != nil {
				return nil, errors.New("cannot save remote credential")
			}
			r.TokenConfigured = true
			s.Entries[name] = r
			if err = s.save(); err != nil {
				_ = s.removeCredential(ctx, r)
				return nil, errors.New("cannot save remote configuration")
			}
			if err = s.removeCredential(ctx, old); err != nil {
				return nil, errors.New("token updated; old credential cleanup failed")
			}
		case "remote token unset", "remote remove":
			if err := s.removeCredential(ctx, r); err != nil {
				return nil, errors.New("cannot remove remote credential")
			}
			r.Credential = ""
			r.Store = ""
			r.TokenConfigured = false
			if inv.Command.Name == "remote remove" {
				delete(s.Entries, name)
			} else {
				s.Entries[name] = r
			}
			if err := s.save(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown remote command")
		}
		return visibleRemote(r), nil
	})
}
