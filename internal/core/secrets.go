package core

import (
	"strings"

	"github.com/mdenesfe/TableF/internal/driver"
)

// secretOptions are the engine options that hold credentials.
var secretOptions = []string{"apiToken", "authToken", "credentialsJSON", "secretAccessKey", "sessionToken", "apiKey"}

// Names used in driver.Config.Secrets.
const (
	secretPassword      = "password"
	secretSSHPassword   = "sshPassword"
	secretSSHPassphrase = "sshPassphrase"
	secretURIPassword   = "options.uri.password"
)

// redact blanks every secret of cfg and lists the ones that were set in
// cfg.Secrets, so the UI can show "saved" without ever receiving them.
func redact(cfg driver.Config) driver.Config {
	var set []string
	take := func(name string, v *string) {
		if *v != "" {
			set = append(set, name)
			*v = ""
		}
	}
	take(secretPassword, &cfg.Password)
	take(secretSSHPassword, &cfg.SSHPassword)
	take(secretSSHPassphrase, &cfg.SSHPassphrase)
	if len(cfg.Options) > 0 {
		opts := make(map[string]string, len(cfg.Options))
		for k, v := range cfg.Options {
			opts[k] = v
		}
		for _, k := range secretOptions {
			if opts[k] != "" {
				set = append(set, "options."+k)
				opts[k] = ""
			}
		}
		if uri, ok := opts["uri"]; ok {
			if stripped, had := stripURIPassword(uri); had {
				set = append(set, secretURIPassword)
				opts["uri"] = stripped
			}
		}
		cfg.Options = opts
	}
	cfg.Secrets = set
	return cfg
}

// mergeSecrets fills every secret that is empty in cfg from stored, the
// saved version of the same connection, so a connection edited in the UI
// (which never receives secrets) keeps them unless they are retyped.
func mergeSecrets(cfg, stored driver.Config) driver.Config {
	fill := func(v *string, old string) {
		if *v == "" {
			*v = old
		}
	}
	fill(&cfg.Password, stored.Password)
	fill(&cfg.SSHPassword, stored.SSHPassword)
	fill(&cfg.SSHPassphrase, stored.SSHPassphrase)
	if len(stored.Options) > 0 {
		opts := make(map[string]string, len(cfg.Options))
		for k, v := range cfg.Options {
			opts[k] = v
		}
		for _, k := range secretOptions {
			if opts[k] == "" && stored.Options[k] != "" {
				opts[k] = stored.Options[k]
			}
		}
		if uri := opts["uri"]; uri != "" {
			opts["uri"] = restoreURIPassword(uri, stored.Options["uri"])
		}
		cfg.Options = opts
	}
	cfg.Secrets = nil
	return cfg
}

// uriUserinfo locates the userinfo of a URI like mongodb://user:pass@host/db,
// returning the byte range of the password (after the ':', before the '@').
// Multi-host URIs (h1,h2) are fine: only the part before the first '/', '?'
// or '#' after the scheme is searched, and the last '@' in it ends userinfo.
func uriUserinfo(uri string) (userStart, colon, at int, ok bool) {
	i := strings.Index(uri, "://")
	if i < 0 {
		return 0, 0, 0, false
	}
	userStart = i + 3
	end := len(uri)
	if j := strings.IndexAny(uri[userStart:], "/?#"); j >= 0 {
		end = userStart + j
	}
	at = strings.LastIndexByte(uri[userStart:end], '@')
	if at < 0 {
		return 0, 0, 0, false
	}
	at += userStart
	colon = strings.IndexByte(uri[userStart:at], ':')
	if colon < 0 {
		colon = at // user without password
	} else {
		colon += userStart
	}
	return userStart, colon, at, true
}

// stripURIPassword removes the password from a URI's userinfo, keeping the
// user name. It reports whether there was a password.
func stripURIPassword(uri string) (string, bool) {
	_, colon, at, ok := uriUserinfo(uri)
	if !ok || colon == at || colon+1 == at {
		return uri, false
	}
	return uri[:colon] + uri[at:], true
}

// restoreURIPassword puts the stored URI's password back into uri when uri
// has a user but no password and both name the same user.
func restoreURIPassword(uri, stored string) string {
	start, colon, at, ok := uriUserinfo(uri)
	if !ok || colon != at {
		return uri // no userinfo, or a password was typed
	}
	sStart, sColon, sAt, ok := uriUserinfo(stored)
	if !ok || sColon == sAt || uri[start:colon] != stored[sStart:sColon] {
		return uri
	}
	return uri[:colon] + stored[sColon:sAt] + uri[at:]
}
