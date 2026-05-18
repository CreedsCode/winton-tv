// Package livekit wraps the LiveKit server SDK: JWT signing, Ingress
// provisioning (WHIP), and live-status polling. The browser viewer JS
// SDK connects to the *public* WS URL; the backend uses the *internal*
// HTTP URL (translated from ws://) for Twirp RPC calls.
package livekit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Client struct {
	publicURL string
	apiKey    string
	apiSecret string
	room      *lksdk.RoomServiceClient
	ingress   *lksdk.IngressClient
}

type Config struct {
	URL        string // backend-facing, ws:// or wss://
	PublicURL  string // browser-facing, wss://
	APIKey     string
	APISecret  string
	IngressURL string // backend Twirp API, http://ingress:9090
}

func New(c Config) *Client {
	httpURL := wsToHTTP(c.URL)
	return &Client{
		publicURL: c.PublicURL,
		apiKey:    c.APIKey,
		apiSecret: c.APISecret,
		room:      lksdk.NewRoomServiceClient(httpURL, c.APIKey, c.APISecret),
		ingress:   lksdk.NewIngressClient(c.IngressURL, c.APIKey, c.APISecret),
	}
}

func (c *Client) PublicURL() string { return c.publicURL }

// ─────────────────────── Tokens ───────────────────────

// ViewerToken mints a viewer JWT for `room`. Subscribe-only, with the
// data-channel allowed so chat works.
func (c *Client) ViewerToken(identity, room string, ttl time.Duration) (string, error) {
	at := auth.NewAccessToken(c.apiKey, c.apiSecret)
	canPub, canSub, canData := false, true, true
	at.SetIdentity(identity).
		SetName(identity).
		AddGrant(&auth.VideoGrant{
			Room:           room,
			RoomJoin:       true,
			CanPublish:     &canPub,
			CanSubscribe:   &canSub,
			CanPublishData: &canData,
		}).
		SetValidFor(ttl)
	return at.ToJWT()
}

// ─────────────────────── Ingress (WHIP for OBS) ───────────────────────

// IngressCredentials are what the dashboard hands to the streamer.
type IngressCredentials struct {
	IngressID string
	StreamKey string
	WhipURL   string
}

// CreateWHIPIngress provisions a WHIP-input ingress that pushes into the
// user's room (room name = slug, identity = slug). Returns OBS-paste-ready
// credentials.
func (c *Client) CreateWHIPIngress(ctx context.Context, slug string) (*IngressCredentials, error) {
	resp, err := c.ingress.CreateIngress(ctx, &livekit.CreateIngressRequest{
		InputType:           livekit.IngressInput_WHIP_INPUT,
		Name:                "winton-" + slug,
		RoomName:            slug,
		ParticipantIdentity: slug,
		ParticipantName:     slug,
	})
	if err != nil {
		return nil, fmt.Errorf("create ingress: %w", err)
	}
	return &IngressCredentials{
		IngressID: resp.IngressId,
		StreamKey: resp.StreamKey,
		WhipURL:   resp.Url,
	}, nil
}

// DeleteIngress is best-effort — "not found" is swallowed.
func (c *Client) DeleteIngress(ctx context.Context, ingressID string) error {
	_, err := c.ingress.DeleteIngress(ctx, &livekit.DeleteIngressRequest{IngressId: ingressID})
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// ─────────────────────── Room status ───────────────────────

// IsLive returns true if `room` has at least one publishing participant.
// Room-not-found is treated as offline (not an error).
func (c *Client) IsLive(ctx context.Context, room string) (bool, error) {
	resp, err := c.room.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, p := range resp.Participants {
		if len(p.Tracks) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// ─────────────────────── helpers ───────────────────────

func wsToHTTP(u string) string {
	u = strings.Replace(u, "wss://", "https://", 1)
	u = strings.Replace(u, "ws://", "http://", 1)
	return u
}

func isNotFound(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "no_such_room")
}
