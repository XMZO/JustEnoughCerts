package contracts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
)

// NoteTest is the event of a Note that only checks a notifier works.
const NoteTest = "notify.test"

// Note is a message for people about an event, written in the language of
// the notifier that sends it.
type Note struct {
	// Event is the type of the event, such as certificate.issue_failed, or
	// NoteTest.
	Event string `json:"event"`
	// Title says what happened in a line; Text says more, in lines.
	Title string `json:"title"`
	Text  string `json:"text"`
	// Subject is Title marked as JEC's, for where the notes of many senders
	// meet, such as a mailbox or a group chat.
	Subject string `json:"subject"`
	// Link is the page about it in JEC; empty when JEC does not know the
	// URL it is reached at.
	Link string    `json:"link,omitempty"`
	Time time.Time `json:"time"`
	// Data is the event's payload, for programs such as a webhook's.
	Data json.RawMessage `json:"data,omitempty"`
}

// Notifier sends notes where people read them: a chat, a mailbox, another
// program. Notify fails with a Problem saying what went wrong, marked
// retry.Permanent when trying again cannot help, such as a rejected token.
type Notifier interface {
	Notify(ctx context.Context, n Note) error
	Close() error
}

// NotifyEvent is an event a notifier can be told about, by the switch Key
// of its configuration.
type NotifyEvent struct {
	Type    string
	Key     string
	Label   i18n.Key
	Default bool
}

// NotifyEvents are the events notifiers can be told about, in the order
// their forms list them: the failures and the expiry, which need someone,
// are on by default.
var NotifyEvents = []NotifyEvent{
	{Type: CertificateIssueFailed, Key: "on_issue_failed", Label: i18n.KernelNotifyEventIssueFailed, Default: true},
	{Type: CertificateDeployFailed, Key: "on_deploy_failed", Label: i18n.KernelNotifyEventDeployFailed, Default: true},
	{Type: CertificateExpiring, Key: "on_expiring", Label: i18n.KernelNotifyEventExpiring, Default: true},
	{Type: CertificateReminder, Key: "on_reminder", Label: i18n.KernelNotifyEventReminder, Default: true},
	{Type: CertificateServedOld, Key: "on_served_old", Label: i18n.KernelNotifyEventServedOld, Default: true},
	{Type: CertificateChainIncomplete, Key: "on_chain_incomplete", Label: i18n.KernelNotifyEventChainIncomplete, Default: true},
	{Type: CertificateRevoked, Key: "on_revoked", Label: i18n.KernelNotifyEventRevoked, Default: true},
	{Type: SiteExpiring, Key: "on_site_expiring", Label: i18n.KernelNotifyEventSiteExpiring, Default: true},
	{Type: DomainExpiring, Key: "on_domain_expiring", Label: i18n.KernelNotifyEventDomainExpiring, Default: true},
	{Type: CertificateSpotted, Key: "on_spotted", Label: i18n.KernelNotifyEventSpotted, Default: true},
	{Type: ProviderFailing, Key: "on_provider_failing", Label: i18n.KernelNotifyEventProviderFailing, Default: true},
	{Type: AccountLocked, Key: "on_locked", Label: i18n.KernelNotifyEventLocked, Default: true},
	{Type: ReleaseAvailable, Key: "on_release", Label: i18n.KernelNotifyEventRelease, Default: true},
	{Type: CertificateIssued, Key: "on_issued", Label: i18n.KernelNotifyEventIssued},
	{Type: CertificateDeployed, Key: "on_deployed", Label: i18n.KernelNotifyEventDeployed},
	{Type: CertificatesDigest, Key: "on_digest", Label: i18n.KernelNotifyEventDigest},
	{Type: AccountSignedIn, Key: "on_sign_in", Label: i18n.KernelNotifyEventSignIn},
}

// NoteLocale is the setting of a notifier that holds the language of its
// notes.
const NoteLocale = "locale"

// NoteQuiet is the setting of a notifier that holds its quiet hours, in
// the panel's time zone, as "22-7": a note that comes then waits for their
// end. QuietOff is none.
const (
	NoteQuiet = "quiet_hours"
	QuietOff  = "off"
)

// quietHours are the choices of quiet hours.
var quietHours = []providers.Option{
	{Value: QuietOff, Label: i18n.KernelNotifyQuietOff},
	{Value: "22-7", Label: i18n.KernelNotifyQuietH22To7},
	{Value: "23-8", Label: i18n.KernelNotifyQuietH23To8},
	{Value: "0-8", Label: i18n.KernelNotifyQuietH0To8},
}

// NotifierFields are the settings every notifier type ends with: the
// language of its notes, a switch for each of NotifyEvents, and the quiet
// hours.
func NotifierFields() []providers.Field {
	out := []providers.Field{{Key: NoteLocale, Label: i18n.KernelNotifyFieldLocale, Kind: providers.Locale, Required: true, Default: i18n.BaseLocale.String()}}
	for _, e := range NotifyEvents {
		def := "false"
		if e.Default {
			def = "true"
		}
		out = append(out, providers.Field{Key: e.Key, Label: e.Label, Kind: providers.Switch, Default: def})
	}
	return append(out, providers.Field{
		Key: NoteQuiet, Label: i18n.KernelNotifyFieldQuiet, Help: i18n.KernelNotifyFieldQuietHelp, Kind: providers.Select,
		Default: QuietOff, Options: quietHours,
	})
}
