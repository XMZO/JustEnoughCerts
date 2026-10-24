package ssh

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/certfiles"
	"github.com/XMZO/JustEnoughCerts/internal/lib/credentials"
)

// Layouts of the remote directory, as the files module has them.
const (
	byDomain = "by_domain"
	flat     = "flat"
)

// maxOutput bounds the output of a command kept in the log.
const maxOutput = 4000

// connFields are the settings of how to reach a machine.
func connFields() []providers.Field {
	return []providers.Field{
		{Key: "host", Label: i18n.SshFieldHost, Kind: providers.Text, Required: true},
		{Key: "port", Label: i18n.SshFieldPort, Kind: providers.Number, Default: "22"},
		{Key: "user", Label: i18n.SshFieldUser, Kind: providers.Text, Required: true},
		{Key: "private_key", Label: i18n.SshFieldPrivateKey, Help: i18n.SshFieldPrivateKeyHelp, Kind: providers.Multiline, Secret: true},
		{Key: "passphrase", Label: i18n.SshFieldPassphrase, Kind: providers.Text, Secret: true, Advanced: true},
		{Key: "password", Label: i18n.SshFieldPassword, Help: i18n.SshFieldPasswordHelp, Kind: providers.Text, Secret: true},
		{Key: "host_key", Label: i18n.SshFieldHostKey, Help: i18n.SshFieldHostKeyHelp, Kind: providers.Text},
	}
}

func serverType() providers.Type {
	fields := append(connFields(),
		providers.Field{Key: "directory", Label: i18n.SshFieldDirectory, Help: i18n.SshFieldDirectoryHelp, Kind: providers.Text, Required: true},
		providers.Field{Key: "layout", Label: i18n.SshFieldLayout, Kind: providers.Select, Required: true, Default: byDomain, Options: []providers.Option{
			{Value: byDomain, Label: i18n.SshLayoutByDomain},
			{Value: flat, Label: i18n.SshLayoutFlat},
		}},
		providers.Field{Key: "command", Label: i18n.SshFieldCommand, Help: i18n.SshFieldCommandHelp, Kind: providers.Text},
	)
	fields = append(fields, certfiles.Fields()...)
	fields = append(fields, providers.Field{Key: "owner", Label: i18n.SshFieldOwner, Help: i18n.SshFieldOwnerHelp, Kind: providers.Text, Advanced: true})
	return providers.Type{
		ID: "ssh.server", Kind: providers.Target, Name: i18n.SshServerName, Description: i18n.SshServerDescription,
		Tags: []string{"self_hosted"}, Testable: true, Fields: fields,
		Credential: credentials.Use(credentials.SSHKey),
	}
}

// ownerPattern is how chown takes an owner: user, user:group or :group.
var ownerPattern = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+(?::[A-Za-z0-9._-]+)?|:[A-Za-z0-9._-]+)$`)

// conn is how to reach a machine over SSH, for the server target and the
// web root solver alike.
type conn struct {
	addr, user, hostKey string
	auth                []gossh.AuthMethod
}

// server is a machine reached over SSH, where certificates are written as
// certbot lays them out, unless the files are set otherwise.
type server struct {
	conn
	directory, layout string
	command           string
	files             certfiles.Files
	owner             string // as chown takes it; "" leaves the files to the user signed in
}

var (
	_ contracts.Target = (*server)(nil)
	_ contracts.Tester = (*server)(nil)
)

func factory(_ context.Context, in providers.Instance) (any, error) {
	c, err := newConn(in)
	if err != nil {
		return nil, err
	}
	files, err := certfiles.Parse(in.Config, in.Secrets)
	if err != nil {
		return nil, err
	}
	owner := strings.TrimSpace(in.Config["owner"])
	if owner != "" && !ownerPattern.MatchString(owner) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorOwner, Params: map[string]any{"owner": owner}})
	}
	return &server{
		conn:      c,
		directory: strings.TrimSpace(in.Config["directory"]), layout: in.Config["layout"],
		command: strings.TrimSpace(in.Config["command"]), files: files, owner: owner,
	}, nil
}

// newConn reads the settings of connFields.
func newConn(in providers.Instance) (conn, error) {
	c := conn{
		addr:    net.JoinHostPort(strings.TrimSpace(in.Config["host"]), cmp.Or(strings.TrimSpace(in.Config["port"]), "22")),
		user:    strings.TrimSpace(in.Config["user"]),
		hostKey: strings.TrimSpace(in.Config["host_key"]),
	}
	if key := strings.TrimSpace(in.Value("private_key")); key != "" {
		signer, err := parseKey([]byte(key), in.Value("passphrase"))
		if err != nil {
			return c, err
		}
		c.auth = append(c.auth, gossh.PublicKeys(signer))
	}
	if pw := in.Value("password"); pw != "" {
		c.auth = append(c.auth, gossh.Password(pw))
	}
	if len(c.auth) == 0 {
		return c, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorNoAuth})
	}
	return c, nil
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

// Test connects, checks the host key and signs in. With no host key set,
// it fails showing the server's, for someone to check.
func (c *conn) Test(ctx context.Context) (contracts.Message, error) {
	client, err := c.dial(ctx)
	if err != nil {
		return contracts.Message{}, err
	}
	_ = client.Close()
	return contracts.Message{Key: i18n.SshTestOk, Params: map[string]any{"user": c.user, "address": c.addr}}, nil
}

// How long the steps of a deployment may take: writing the files, and the
// command run afterwards, which may wait for a service to reload, as
// systemctl reload does; tests shorten them.
var (
	writeTimeout   = 2 * time.Minute
	commandTimeout = 5 * time.Minute
)

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
	files, err := s.files.Make(req.Domains, req.Chain, req.Key)
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
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	write := func(command string, stdin []byte) error {
		out, err := run(wctx, client, command, stdin)
		if err != nil && ctx.Err() == nil && wctx.Err() != nil {
			return &contracts.Problem{Key: i18n.SshErrorWriteTimeout, Params: map[string]any{
				"directory": dir, "address": s.addr, "minutes": int(writeTimeout / time.Minute),
			}, Err: err}
		}
		if err != nil {
			return s.writeFailed(ctx, dir, out, err)
		}
		return nil
	}
	if err := write("mkdir -p "+quote(dir), nil); err != nil {
		return none, err
	}
	var moves []string
	for _, f := range files {
		// Readable by the owner only until it has its mode and owner.
		tmp := path.Join(dir, "."+f.Name+".jec")
		script := "umask 077 && cat > " + quote(tmp) + " && chmod " + fmt.Sprintf("%04o", f.Mode) + " " + quote(tmp)
		if s.owner != "" {
			script += " && chown " + quote(s.owner) + " " + quote(tmp)
		}
		if err := write(script, f.Data); err != nil {
			return none, err
		}
		moves = append(moves, "mv -f "+quote(tmp)+" "+quote(path.Join(dir, f.Name)))
	}
	if err := write(strings.Join(moves, " && "), nil); err != nil {
		return none, err
	}

	if s.command != "" {
		log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogCommand, Params: map[string]any{"command": s.command}})
		cctx, cancel := context.WithTimeout(ctx, commandTimeout)
		out, err := run(cctx, client, s.command, nil)
		late := errors.Is(cctx.Err(), context.DeadlineExceeded)
		cancel()
		if len(out) > 0 {
			log(contracts.LogLine{Level: contracts.Info, Key: i18n.SshLogOutput, Detail: string(tail(out))})
		}
		if err != nil {
			if ctx.Err() != nil {
				return none, ctx.Err()
			}
			if late {
				// Running it again would most likely wait as long.
				return none, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorCommandTimeout, Params: map[string]any{
					"command": s.command, "directory": dir, "minutes": int(commandTimeout / time.Minute),
				}, Err: err})
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
func (c *conn) dial(ctx context.Context) (*gossh.Client, error) {
	var seen string // the fingerprint the server showed
	config := &gossh.ClientConfig{
		User: c.user, Auth: c.auth, Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
			seen = gossh.FingerprintSHA256(key)
			if !c.knows(key) {
				return errHostKey
			}
			return nil
		},
	}
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": c.addr}, Err: err}
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Minute {
		deadline = time.Now().Add(time.Minute)
	}
	_ = conn.SetDeadline(deadline) // for the handshake; cleared once in
	cc, chans, reqs, err := gossh.NewClientConn(conn, c.addr, config)
	if err != nil {
		_ = conn.Close()
		switch {
		case errors.Is(err, errHostKey) && c.hostKey == "":
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorHostKeyUnknown, Params: map[string]any{"fingerprint": seen}})
		case errors.Is(err, errHostKey):
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorHostKey, Params: map[string]any{"fingerprint": seen}})
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, retry.Permanent(&contracts.Problem{Key: i18n.SshErrorAuth, Params: map[string]any{"user": c.user, "address": c.addr}, Err: err})
		}
		return nil, &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": c.addr}, Err: err}
	}
	_ = conn.SetDeadline(time.Time{})
	return gossh.NewClient(cc, chans, reqs), nil
}

var errHostKey = errors.New("ssh: the host key is not the one set")

// knows reports whether key is the host key set: its SHA-256
// fingerprint, as ssh-keygen -l shows it, or the key itself, as in
// known_hosts or a .pub file.
func (c *conn) knows(key gossh.PublicKey) bool {
	set := c.hostKey
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
func (c *conn) writeFailed(ctx context.Context, dir string, out []byte, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *gossh.ExitError
	if errors.As(err, &exit) {
		return retry.Permanent(&contracts.Problem{Key: i18n.SshErrorWrite, Params: map[string]any{"directory": dir, "output": strings.TrimSpace(string(tail(out)))}, Err: err})
	}
	return &contracts.Problem{Key: i18n.SshErrorConnect, Params: map[string]any{"address": c.addr}, Err: err}
}

// run runs command in a session of its own, with stdin as its input, and
// returns what it wrote to stdout and stderr. When ctx ends first, it
// stops waiting, with ctx's error: the command is sent SIGTERM and the
// session closed, though a server may let it go on.
func run(ctx context.Context, c *gossh.Client, command string, stdin []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if err := session.Start(command); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case err = <-done:
		return out.bytes(), err
	case <-ctx.Done():
		_ = session.Signal(gossh.SIGTERM)
		return out.bytes(), ctx.Err()
	}
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
var dirName = certfiles.DomainName
