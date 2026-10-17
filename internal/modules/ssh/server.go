package ssh

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/cryptox"
)

// Layouts of the remote directory, as the files module has them.
const (
	byDomain = "by_domain"
	flat     = "flat"
)

// maxOutput bounds the output of a command kept in the log.
const maxOutput = 4000

func serverType() providers.Type {
	return providers.Type{
		ID: "ssh.server", Kind: providers.Target, Name: i18n.SshServerName, Description: i18n.SshServerDescription,
		Tags: []string{"self_hosted"},
		Fields: []providers.Field{
			{Key: "host", Label: i18n.SshFieldHost, Kind: providers.Text, Required: true},
			{Key: "port", Label: i18n.SshFieldPort, Kind: providers.Number, Default: "22"},
			{Key: "user", Label: i18n.SshFieldUser, Kind: providers.Text, Required: true},
			{Key: "private_key", Label: i18n.SshFieldPrivateKey, Help: i18n.SshFieldPrivateKeyHelp, Kind: providers.Multiline, Secret: true},
			{Key: "passphrase", Label: i18n.SshFieldPassphrase, Kind: providers.Text, Secret: true, Advanced: true},
			{Key: "password", Label: i18n.SshFieldPassword, Help: i18n.SshFieldPasswordHelp, Kind: providers.Text, Secret: true},
			{Key: "host_key", Label: i18n.SshFieldHostKey, Help: i18n.SshFieldHostKeyHelp, Kind: providers.Text},
			{Key: "directory", Label: i18n.SshFieldDirectory, Help: i18n.SshFieldDirectoryHelp, Kind: providers.Text, Required: true},
			{Key: "layout", Label: i18n.SshFieldLayout, Kind: providers.Select, Required: true, Default: byDomain, Options: []providers.Option{
				{Value: byDomain, Label: i18n.SshLayoutByDomain},
				{Value: flat, Label: i18n.SshLayoutFlat},
			}},
			{Key: "command", Label: i18n.SshFieldCommand, Help: i18n.SshFieldCommandHelp, Kind: providers.Text},
		},
	}
}

// server is a machine reached over SSH, where certificates are written as
// certbot lays them out.
type server struct {
	addr, user, hostKey string
	auth                []gossh.AuthMethod
	directory, layout   string
	command             string
}

var _ contracts.Target = (*server)(nil)

func factory(_ context.Context, in providers.Instance) (any, error) {
	s := &server{
		addr:      net.JoinHostPort(strings.TrimSpace(in.Config["host"]), cmp.Or(strings.TrimSpace(in.Config["port"]), "22")),
		user:      strings.TrimSpace(in.Config["user"]),
		hostKey:   strings.TrimSpace(in.Config["host_key"]),
		directory: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"],
		command: strings.TrimSpace(in.Config["command"]),
	}
	if key := strings.TrimSpace(in.Value("private_key")); key != "" {
		signer, err := parseKey([]byte(key), in.Value("passphrase"))
		if err != nil {
			return nil, err
		}
		s.auth = append(s.auth, gossh.PublicKeys(signer))
	}
	if pw := in.Value("password"); pw != "" {
		s.auth = append(s.auth, gossh.Password(pw))
	}
	if len(s.auth) == 0 {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorNoAuth})
	}
	return s, nil
}

// parseKey reads a private key in OpenSSH or PEM form.
func parseKey(pem []byte, passphrase string) (gossh.Signer, error) {
	if passphrase != "" {
		signer, err := gossh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
		if err != nil {
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorKeyOrPassphrase, Err: err})
		}
		return signer, nil
	}
	signer, err := gossh.ParsePrivateKey(pem)
	var missing *gossh.PassphraseMissingError
	switch {
	case errors.As(err, &missing):
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorPassphrase, Err: err})
	case err != nil:
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorKey, Err: err})
	}
	return signer, nil
}

// Close has nothing to release: each deployment has a connection of its
// own.
func (*server) Close() error { return nil }

// Deploy writes the files to temporary names, renames them over the old
// ones in one command, and then runs the command set to run afterwards.
// The files are overwritten, so there is nothing to remove later. The
// server needs a POSIX shell, as every Linux and BSD has.
func (s *server) Deploy(ctx context.Context, req contracts.DeployRequest) (contracts.DeployResult, error) {
	var none contracts.DeployResult
	log := req.Log
	if log == nil {
		log = func(contracts.LogLine) {}
	}
	if !path.IsAbs(s.directory) {
		return none, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorRelative, Params: map[string]any{"directory": s.directory}})
	}
	if len(req.Chain) == 0 {
		return none, retry.Permanent(errors.New("ssh: no certificate to deploy"))
	}
	dir := s.directory
	if s.layout != flat {
		name, err := dirName(req.Domains)
		if err != nil {
			return none, retry.Permanent(err)
		}
		dir = path.Join(dir, name)
	}
	key, err := cryptox.EncodeKey(req.Key)
	if err != nil {
		return none, retry.Permanent(err)
	}

	log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogConnect, Params: map[string]any{"user": s.user, "address": s.addr}})
	client, err := s.dial(ctx)
	if err != nil {
		return none, err
	}
	defer func() { _ = client.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()

	log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogUpload, Params: map[string]any{"directory": dir}})
	files := []struct {
		name  string
		data  []byte
		umask string
	}{
		{"cert.pem", cryptox.EncodeCertificates(req.Chain[0]), "022"},
		{"chain.pem", cryptox.EncodeCertificates(req.Chain[1:]...), "022"},
		{"fullchain.pem", cryptox.EncodeCertificates(req.Chain...), "022"},
		{"privkey.pem", key, "077"},
	}
	if out, err := run(client, "mkdir -p "+quote(dir), nil); err != nil {
		return none, s.writeFailed(ctx, dir, out, err)
	}
	var moves []string
	for _, f := range files {
		tmp := path.Join(dir, "."+f.name+".jec")
		if out, err := run(client, "umask "+f.umask+" && cat > "+quote(tmp), f.data); err != nil {
			return none, s.writeFailed(ctx, dir, out, err)
		}
		moves = append(moves, "mv -f "+quote(tmp)+" "+quote(path.Join(dir, f.name)))
	}
	if out, err := run(client, strings.Join(moves, " && "), nil); err != nil {
		return none, s.writeFailed(ctx, dir, out, err)
	}

	if s.command != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogCommand, Params: map[string]any{"command": s.command}})
		out, err := run(client, s.command, nil)
		if len(out) > 0 {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogOutput, Detail: string(tail(out))})
		}
		if err != nil {
			if ctx.Err() != nil {
				return none, ctx.Err()
			}
			var exit *gossh.ExitError
			if errors.As(err, &exit) {
				return none, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorCommand, Params: map[string]any{"status": exit.ExitStatus()}, Err: err})
			}
			return none, &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": s.addr}, Err: err}
		}
	}
	return none, nil
}

// dial connects and signs in, checking the server's host key.
func (s *server) dial(ctx context.Context) (*gossh.Client, error) {
	var seen string // the fingerprint the server showed
	config := &gossh.ClientConfig{
		User: s.user, Auth: s.auth, Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
			seen = gossh.FingerprintSHA256(key)
			if !s.knows(key) {
				return errHostKey
			}
			return nil
		},
	}
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": s.addr}, Err: err}
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Minute {
		deadline = time.Now().Add(time.Minute)
	}
	_ = conn.SetDeadline(deadline) // for the handshake; cleared once in
	c, chans, reqs, err := gossh.NewClientConn(conn, s.addr, config)
	if err != nil {
		_ = conn.Close()
		switch {
		case errors.Is(err, errHostKey) && s.hostKey == "":
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorHostKeyUnknown, Params: map[string]any{"fingerprint": seen}})
		case errors.Is(err, errHostKey):
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorHostKey, Params: map[string]any{"fingerprint": seen}})
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorAuth, Params: map[string]any{"user": s.user, "address": s.addr}, Err: err})
		}
		return nil, &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": s.addr}, Err: err}
	}
	_ = conn.SetDeadline(time.Time{})
	return gossh.NewClient(c, chans, reqs), nil
}

var errHostKey = errors.New("ssh: the host key is not the one set")

// knows reports whether key is the host key set: its SHA-256
// fingerprint, as ssh-keygen -l shows it, or the key itself, as in
// known_hosts or a .pub file.
func (s *server) knows(key gossh.PublicKey) bool {
	set := s.hostKey
	if set == "" {
		return false
	}
	if strings.HasPrefix(set, "SHA256:") {
		return strings.TrimRight(set, "=") == strings.TrimRight(gossh.FingerprintSHA256(key), "=")
	}
	// known_hosts puts the host names first.
	fields := strings.Fields(set)
	for i := range fields {
		k, _, _, _, err := gossh.ParseAuthorizedKey([]byte(strings.Join(fields[i:], " ")))
		if err == nil {
			return bytes.Equal(k.Marshal(), key.Marshal())
		}
	}
	return false
}

// writeFailed explains a failure to write the files: a refusal of the
// server, such as a missing permission, ends the deployment.
func (s *server) writeFailed(ctx context.Context, dir string, out []byte, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *gossh.ExitError
	if errors.As(err, &exit) {
		return retry.Permanent(&contracts.Problem{Key: i18n.SshErrorWrite, Params: map[string]any{"directory": dir, "output": strings.TrimSpace(string(tail(out)))}, Err: err})
	}
	return &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": s.addr}, Err: err}
}

// run runs command in a session of its own, with stdin as its input, and
// returns what it wrote to stdout and stderr.
func run(c *gossh.Client, command string, stdin []byte) ([]byte, error) {
	session, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	var out output
	session.Stdout, session.Stderr = &out, &out
	if stdin != nil {
		session.Stdin = bytes.NewReader(stdin)
	}
	err = session.Run(command)
	return out.bytes(), err
}

// output collects what a command writes, which comes from two goroutines,
// one copying stdout and one stderr.
type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *output) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Bytes()
}

// quote quotes s for a POSIX shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// tail keeps the end of a command's output.
func tail(out []byte) []byte {
	if len(out) > maxOutput {
		return out[len(out)-maxOutput:]
	}
	return out
}

// dirName names the subdirectory of a certificate after its primary
// domain: *.example.com becomes star.example.com.
func dirName(domains []string) (string, error) {
	if len(domains) == 0 {
		return "", errors.New("ssh: a certificate without domains")
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '_'
	}, strings.Replace(strings.ToLower(domains[0]), "*", "star", 1))
	if strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("ssh: %q makes no directory name", domains[0])
	}
	return name, nil
}
