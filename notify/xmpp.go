package notify

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"net"
	"time"

	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

// XMPPNotifier sends each alert over a fresh XMPP session using mellium.im/xmpp
// — the same library the `msg` tool uses — so authentication (SCRAM-SHA-256
// with channel binding, StartTLS) matches what the server expects. Alerts are
// rare, so connect-send-disconnect is simpler and more robust than holding a
// long-lived connection open through network blips.
type XMPPNotifier struct {
	JID        string
	Password   string
	Server     string // host:port; derived from JID domain :5222 if empty
	Recipients []string
}

func (x *XMPPNotifier) Notify(e Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := x.connect(ctx)
	if err != nil {
		return fmt.Errorf("xmpp connect: %w", err)
	}
	defer session.Close()

	// Drain inbound stanzas in the background; without a running handler the
	// session can't make send progress.
	go func() {
		_ = session.Serve(xmpp.HandlerFunc(func(t xmlstream.TokenReadEncoder, _ *xml.StartElement) error {
			_, err := xmlstream.Copy(xmlstream.Discard(), t)
			return err
		}))
	}()

	msg := e.Message()
	var firstErr error
	for _, to := range x.Recipients {
		if err := x.send(ctx, session, to, msg); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (x *XMPPNotifier) connect(ctx context.Context) (*xmpp.Session, error) {
	j, err := jid.Parse(x.JID)
	if err != nil {
		return nil, fmt.Errorf("invalid jid %q: %w", x.JID, err)
	}

	target := x.Server
	if target == "" {
		target = j.Domain().String() + ":5222"
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", target, err)
	}

	features := []xmpp.StreamFeature{
		xmpp.StartTLS(&tls.Config{ServerName: j.Domain().String(), MinVersion: tls.VersionTLS12}),
		xmpp.SASL("", x.Password,
			sasl.ScramSha256Plus, sasl.ScramSha256,
			sasl.ScramSha1Plus, sasl.ScramSha1, sasl.Plain),
		xmpp.BindResource(),
	}

	session, err := xmpp.NewClientSession(ctx, j, conn, features...)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return session, nil
}

func (x *XMPPNotifier) send(ctx context.Context, session *xmpp.Session, to, body string) error {
	toJID, err := jid.Parse(to)
	if err != nil {
		return fmt.Errorf("invalid recipient %q: %w", to, err)
	}

	msg := struct {
		stanza.Message
		Body string `xml:"body"`
	}{
		Message: stanza.Message{To: toJID, Type: stanza.ChatMessage},
		Body:    body,
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return session.Encode(sendCtx, msg)
}
