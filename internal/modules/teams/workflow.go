package teams

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// maxText bounds the text of a card, in characters; Teams takes messages
// up to about 28 KB.
const maxText = 8000

// hostSuffixes are those of the hosts webhooks of Workflows are on: Azure
// Logic Apps, and Power Platform for the newer ones.
var hostSuffixes = []string{".logic.azure.com", ".powerplatform.com", ".powerautomate.com"}

func workflowType() providers.Type {
	return providers.Type{
		ID: "teams.workflow", Kind: providers.Notifier, Name: i18n.TeamsWorkflowName, Description: i18n.TeamsWorkflowDescription, Untested: true,
		Fields: append([]providers.Field{
			{Key: "url", Label: i18n.TeamsFieldUrl, Help: i18n.TeamsFieldUrlHelp, Kind: providers.URL, Secret: true, Required: true},
			httpx.ProxyField(),
		}, contracts.NotifierFields()...),
	}
}

// workflow posts notes to the webhook of a workflow, which posts them as
// cards in Teams.
type workflow struct {
	client *http.Client
	url    string
}

var _ contracts.Notifier = (*workflow)(nil)

// factory reads the webhook URL, which has to be one of Workflows: the
// signature in it goes nowhere else.
func factory(_ context.Context, in providers.Instance) (any, error) {
	u, err := url.Parse(strings.TrimSpace(in.Value("url")))
	if err != nil || u.Scheme != "https" || !hosted(u.Hostname()) {
		return nil, retry.Permanent(&contracts.Problem{Key: i18n.TeamsErrorUrl})
	}
	client, err := httpx.ClientFor(httpx.Options{PublicOnly: true}, in.Config)
	if err != nil {
		return nil, retry.Permanent(err)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &workflow{client: client, url: u.String()}, nil
}

func hosted(host string) bool {
	for _, s := range hostSuffixes {
		if strings.HasSuffix(strings.ToLower(host), s) {
			return true
		}
	}
	return false
}

// Close lets go of idle connections.
func (w *workflow) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

// color is the color of the title of a card about event, as Adaptive Cards
// name them.
func color(event string) string {
	switch event {
	case contracts.CertificateIssueFailed, contracts.CertificateDeployFailed:
		return "attention"
	case contracts.CertificateExpiring, contracts.CertificateServedOld, contracts.CertificateChainIncomplete,
		contracts.CertificateSpotted, contracts.ProviderFailing:
		return "warning"
	case contracts.CertificateIssued, contracts.CertificateDeployed:
		return "good"
	}
	return "default"
}

// card is the Adaptive Card of n: its subject as the title, a link to the
// page in JEC when there is one, and a block for each line of its text, as
// Teams joins the lines of one block.
func card(n contracts.Note) map[string]any {
	title := cmp.Or(n.Subject, n.Title)
	if n.Link != "" {
		title = "[" + strings.NewReplacer("[", "(", "]", ")").Replace(title) + "](" + n.Link + ")"
	}
	body := []any{map[string]any{"type": "TextBlock", "text": title, "weight": "Bolder", "size": "Medium", "wrap": true, "color": color(n.Event)}}
	text := n.Text
	if utf8.RuneCountInString(text) > maxText {
		text = string([]rune(text)[:maxText-1]) + "…"
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) != "" {
			body = append(body, map[string]any{"type": "TextBlock", "text": line, "wrap": true, "spacing": "Small"})
		}
	}
	return map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard", "version": "1.4", "body": body,
			},
		}},
	}
}

// Notify posts n as a card.
func (w *workflow) Notify(ctx context.Context, n contracts.Note) error {
	body, err := json.Marshal(card(n))
	if err != nil {
		return retry.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		// The URL holds the signature: only the cause goes on.
		return &contracts.Problem{Key: i18n.TeamsErrorNetwork, Err: httpx.Cause(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	return explain(resp)
}

// explain turns the answer of Workflows into an error: one that trying
// again cannot fix, such as a workflow turned off, ends the attempts.
func explain(resp *http.Response) error {
	status := resp.StatusCode
	if status/100 == 2 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var reply struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &reply) == nil && reply.Error.Message != "" {
		msg = reply.Error.Message
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	p := &contracts.Problem{Key: i18n.TeamsErrorApi, Params: map[string]any{"status": status, "message": cmp.Or(msg, http.StatusText(status))}}
	switch {
	case status == http.StatusTooManyRequests:
		s, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return retry.After(p, time.Duration(s)*time.Second)
	case status >= 500:
		return p
	}
	return retry.Permanent(p)
}
